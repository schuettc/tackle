package observe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/docket/item"
)

const ownerQuery = `query($login: String!, $after: String) {
  repositoryOwner(login: $login) {
    repositories(first: 50, after: $after, ownerAffiliations: [OWNER], orderBy: {field: PUSHED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        nameWithOwner isArchived isFork isPrivate pushedAt
        parent { nameWithOwner }
        defaultBranchRef { name }
        latestRelease { publishedAt }
        pullRequests(states: OPEN, first: 50, orderBy: {field: UPDATED_AT, direction: DESC}) {
          pageInfo { hasNextPage }
          nodes { number title url state isDraft createdAt updatedAt author { login } comments(last: 1) { nodes { author { login } createdAt } } }
        }
        issues(states: OPEN, first: 50, orderBy: {field: UPDATED_AT, direction: DESC}) {
          pageInfo { hasNextPage }
          nodes { number title url state createdAt updatedAt author { login } comments(last: 1) { nodes { author { login } createdAt } } }
        }
      }
    }
  }
}`

const searchQuery = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 100, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { number title url state isDraft createdAt updatedAt author { login } repository { nameWithOwner } comments(last: 1) { nodes { author { login } createdAt } } } }
  }
}`

const (
	maxOwnerPages  = 20
	maxSearchPages = 10
	refBatch       = 40
)

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type loginNode struct {
	Login string `json:"login"`
}

type gqlPR struct {
	Number     int        `json:"number"`
	Title      string     `json:"title"`
	URL        string     `json:"url"`
	State      string     `json:"state"`
	IsDraft    bool       `json:"isDraft"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	Author     *loginNode `json:"author"`
	Repository *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Comments struct {
		Nodes []struct {
			Author    *loginNode `json:"author"`
			CreatedAt time.Time  `json:"createdAt"`
		} `json:"nodes"`
	} `json:"comments"`
}

type gqlConn struct {
	PageInfo pageInfo `json:"pageInfo"`
	Nodes    []gqlPR  `json:"nodes"`
}

type gqlRepo struct {
	NameWithOwner string    `json:"nameWithOwner"`
	IsArchived    bool      `json:"isArchived"`
	IsFork        bool      `json:"isFork"`
	IsPrivate     bool      `json:"isPrivate"`
	PushedAt      time.Time `json:"pushedAt"`
	Parent        *struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"parent"`
	DefaultBranchRef *struct {
		Name string `json:"name"`
	} `json:"defaultBranchRef"`
	LatestRelease *struct {
		PublishedAt time.Time `json:"publishedAt"`
	} `json:"latestRelease"`
	PullRequests gqlConn `json:"pullRequests"`
	Issues       gqlConn `json:"issues"`
}

type gqlError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Path    []any  `json:"path"`
}

func rateLimited(err error, errs []gqlError) bool {
	for _, e := range errs {
		if e.Type == "RATE_LIMITED" {
			return true
		}
	}
	var ge *GhError
	return errors.As(err, &ge) && strings.Contains(strings.ToLower(ge.Stderr), "rate limit")
}

func errText(err error, errs []gqlError) string {
	if len(errs) > 0 {
		return errs[0].Type + ": " + errs[0].Message
	}
	if err != nil {
		return err.Error()
	}
	return "empty response"
}

// RefreshOptions controls a refresh.
type RefreshOptions struct {
	Owners []string   // explicit owners; empty = the user plus every org gh lists
	Keys   []item.Key // items to look up individually (decisions, until refs)
	Now    time.Time
}

// RefreshReport lists what could not be refreshed.
type RefreshReport struct {
	Errors      []string
	RateLimited bool
}

// Refresh fetches GitHub state into a copy of prev. Whatever cannot be
// fetched keeps its previous observation, marked stale: absence is not
// evidence.
func Refresh(ctx context.Context, r Runner, prev *GitHub, o RefreshOptions) (*GitHub, RefreshReport) {
	g := clone(prev)
	var rep RefreshReport
	fail := func(format string, a ...any) { rep.Errors = append(rep.Errors, fmt.Sprintf(format, a...)) }

	out, err := r.Gh(ctx, "api", "user", "--jq", ".login")
	user := strings.TrimSpace(string(out))
	if err != nil || user == "" {
		fail("gh unavailable, GitHub observations not refreshed: %v", err)
		for _, ow := range g.Owners {
			ow.Stale = true
		}
		return g, rep
	}
	g.User = user

	owners := o.Owners
	if len(owners) == 0 {
		owners = []string{user}
		out, err := r.Gh(ctx, "api", "user/orgs", "--paginate", "--jq", ".[].login")
		if err != nil {
			fail("listing orgs: %v (using previously known owners)", err)
			for k := range g.Owners {
				if k != strings.ToLower(user) {
					owners = append(owners, g.Owners[k].Login)
				}
			}
		} else {
			owners = append(owners, strings.Fields(string(out))...)
		}
	}

	for _, login := range owners {
		key := strings.ToLower(login)
		ow := g.Owners[key]
		if ow == nil {
			ow = &Owner{Login: login}
			g.Owners[key] = ow
		}
		if rep.RateLimited {
			ow.Stale, ow.Reason = true, "rate limited; retry next sync"
			continue
		}
		if key != strings.ToLower(user) {
			if _, err := r.Gh(ctx, "api", "orgs/"+login+"/repos?per_page=1", "--jq", "length"); err != nil {
				var ge *GhError
				switch {
				case errors.As(err, &ge) && strings.Contains(ge.Stderr, "SAML"):
					ow.Reachable, ow.Stale, ow.Reason = false, true, "SAML SSO: gh's token is not authorized for this org"
					fail("%s: unreachable (SAML SSO)", login)
					continue
				case errors.As(err, &ge) && strings.Contains(ge.Stderr, "HTTP 404"):
					// Not an org (a user account): GraphQL below still works.
				default:
					if rateLimited(err, nil) {
						rep.RateLimited = true
					}
					ow.Stale, ow.Reason = true, err.Error()
					fail("%s: %v", login, err)
					continue
				}
			}
		}
		repos, err := fetchOwner(ctx, r, login, &rep)
		if err != nil {
			ow.Stale, ow.Reason = true, err.Error()
			fail("%s: %v", login, err)
			continue
		}
		ow.Login, ow.Reachable, ow.Stale, ow.Reason, ow.FetchedAt, ow.Repos = login, true, false, "", o.Now.UTC(), repos
	}

	if !rep.RateLimited {
		if prs, err := fetchAuthored(ctx, r, user, &rep); err != nil {
			fail("authored pull requests: %v", err)
		} else {
			g.Authored, g.AuthoredAt = prs, o.Now.UTC()
		}
	}
	if !rep.RateLimited {
		lookupRefs(ctx, r, g, o.Keys, o.Now, &rep, fail)
	}
	return g, rep
}

func clone(prev *GitHub) *GitHub {
	g := NewGitHub()
	if prev == nil {
		return g
	}
	g.User, g.Authored, g.AuthoredAt, g.RefsAt = prev.User, prev.Authored, prev.AuthoredAt, prev.RefsAt
	for k, v := range prev.Owners {
		c := *v
		g.Owners[k] = &c
	}
	for k, v := range prev.Refs {
		g.Refs[k] = v
	}
	return g
}

func fetchOwner(ctx context.Context, r Runner, login string, rep *RefreshReport) ([]RepoObs, error) {
	var repos []RepoObs
	after := ""
	for page := 0; page < maxOwnerPages; page++ {
		args := []string{"api", "graphql", "-f", "query=" + ownerQuery, "-f", "login=" + login}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		out, err := r.Gh(ctx, args...)
		var resp struct {
			Data struct {
				RepositoryOwner *struct {
					Repositories struct {
						PageInfo pageInfo  `json:"pageInfo"`
						Nodes    []gqlRepo `json:"nodes"`
					} `json:"repositories"`
				} `json:"repositoryOwner"`
			} `json:"data"`
			Errors []gqlError `json:"errors"`
		}
		jerr := json.Unmarshal(out, &resp)
		if err != nil || jerr != nil || len(resp.Errors) > 0 || resp.Data.RepositoryOwner == nil {
			if rateLimited(err, resp.Errors) {
				rep.RateLimited = true
				return nil, fmt.Errorf("rate limited; retry next sync")
			}
			return nil, fmt.Errorf("%s", errText(err, resp.Errors))
		}
		for _, n := range resp.Data.RepositoryOwner.Repositories.Nodes {
			repos = append(repos, toRepo(n))
		}
		pi := resp.Data.RepositoryOwner.Repositories.PageInfo
		if !pi.HasNextPage {
			return repos, nil
		}
		after = pi.EndCursor
	}
	return nil, fmt.Errorf("more than %d pages of repositories; listing incomplete", maxOwnerPages)
}

func fetchAuthored(ctx context.Context, r Runner, user string, rep *RefreshReport) ([]PRObs, error) {
	var prs []PRObs
	after := ""
	for page := 0; page < maxSearchPages; page++ {
		args := []string{"api", "graphql", "-f", "query=" + searchQuery, "-f", "q=is:pr is:open archived:false author:" + user}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		out, err := r.Gh(ctx, args...)
		var resp struct {
			Data struct {
				Search *struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []gqlPR  `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
			Errors []gqlError `json:"errors"`
		}
		jerr := json.Unmarshal(out, &resp)
		if err != nil || jerr != nil || len(resp.Errors) > 0 || resp.Data.Search == nil {
			if rateLimited(err, resp.Errors) {
				rep.RateLimited = true
			}
			return nil, fmt.Errorf("%s", errText(err, resp.Errors))
		}
		for _, n := range resp.Data.Search.Nodes {
			if n.Repository != nil {
				prs = append(prs, toPR(n, n.Repository.NameWithOwner))
			}
		}
		if !resp.Data.Search.PageInfo.HasNextPage {
			return prs, nil
		}
		after = resp.Data.Search.PageInfo.EndCursor
	}
	return nil, fmt.Errorf("more than %d pages of authored pull requests", maxSearchPages)
}

func toRepo(n gqlRepo) RepoObs {
	r := RepoObs{Repo: n.NameWithOwner, Archived: n.IsArchived, Fork: n.IsFork, Private: n.IsPrivate, PushedAt: n.PushedAt,
		PRsTruncated: n.PullRequests.PageInfo.HasNextPage, IssuesTruncated: n.Issues.PageInfo.HasNextPage}
	if n.Parent != nil {
		r.Parent = n.Parent.NameWithOwner
	}
	if n.DefaultBranchRef != nil {
		r.DefaultBranch = n.DefaultBranchRef.Name
	}
	if n.LatestRelease != nil {
		r.LatestRelease = n.LatestRelease.PublishedAt
	}
	for _, p := range n.PullRequests.Nodes {
		r.PRs = append(r.PRs, toPR(p, n.NameWithOwner))
	}
	for _, p := range n.Issues.Nodes {
		r.Issues = append(r.Issues, toPR(p, n.NameWithOwner))
	}
	return r
}

func toPR(n gqlPR, repo string) PRObs {
	p := PRObs{Repo: repo, Number: n.Number, Title: n.Title, State: n.State, Draft: n.IsDraft,
		CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt, URL: n.URL, Author: "ghost"}
	if n.Author != nil {
		p.Author = n.Author.Login
	}
	if len(n.Comments.Nodes) > 0 {
		c := n.Comments.Nodes[len(n.Comments.Nodes)-1]
		p.LastCommentAt = c.CreatedAt
		p.LastCommentAuthor = "ghost"
		if c.Author != nil {
			p.LastCommentAuthor = c.Author.Login
		}
	}
	return p
}

// lookupRefs fetches keys individually in batches of aliased, parameterized
// queries. A key whose alias failed for any reason other than NOT_FOUND keeps
// its previous Ref.
func lookupRefs(ctx context.Context, r Runner, g *GitHub, keys []item.Key, now time.Time, rep *RefreshReport, fail func(string, ...any)) {
	var todo []item.Key
	for _, k := range keys {
		if k.Kind != item.KindWorktree {
			todo = append(todo, k)
		}
	}
	anyOK := false
	for start := 0; start < len(todo); start += refBatch {
		end := start + refBatch
		if end > len(todo) {
			end = len(todo)
		}
		batch := todo[start:end]
		var vars, body []string
		args := []string{"api", "graphql"}
		for i, k := range batch {
			vars = append(vars, fmt.Sprintf("$o%d: String!, $n%d: String!", i, i))
			args = append(args, "-f", fmt.Sprintf("o%d=%s", i, k.Owner), "-f", fmt.Sprintf("n%d=%s", i, k.Name))
			sel := "isArchived pushedAt latestRelease { publishedAt }"
			switch k.Kind {
			case item.KindPR:
				vars = append(vars, fmt.Sprintf("$m%d: Int!", i))
				args = append(args, "-F", fmt.Sprintf("m%d=%d", i, k.Number))
				sel = fmt.Sprintf("pullRequest(number: $m%d) { state updatedAt }", i)
			case item.KindIssue:
				vars = append(vars, fmt.Sprintf("$m%d: Int!", i))
				args = append(args, "-F", fmt.Sprintf("m%d=%d", i, k.Number))
				sel = fmt.Sprintf("issue(number: $m%d) { state updatedAt }", i)
			case item.KindBranch:
				vars = append(vars, fmt.Sprintf("$b%d: String!", i))
				args = append(args, "-f", fmt.Sprintf("b%d=refs/heads/%s", i, k.Branch))
				sel = fmt.Sprintf("ref(qualifiedName: $b%d) { name }", i)
			}
			body = append(body, fmt.Sprintf("k%d: repository(owner: $o%d, name: $n%d) { %s }", i, i, i, sel))
		}
		q := "query(" + strings.Join(vars, ", ") + ") {\n  " + strings.Join(body, "\n  ") + "\n}"
		// Build the final args: insert query after "graphql"
		finalArgs := append([]string{"api", "graphql", "-f", "query=" + q}, args[2:]...)
		out, err := r.Gh(ctx, finalArgs...)
		var resp struct {
			Data   map[string]json.RawMessage `json:"data"`
			Errors []gqlError                 `json:"errors"`
		}
		if json.Unmarshal(out, &resp) != nil || resp.Data == nil {
			if rateLimited(err, resp.Errors) {
				rep.RateLimited = true
				fail("looking up %d items: %s", len(batch), errText(err, resp.Errors))
				return // stop: remaining keys keep their previous Refs
			}
			fail("looking up %d items: %s", len(batch), errText(err, resp.Errors))
			continue
		}
		anyOK = true
		for i, k := range batch {
			alias := "k" + strconv.Itoa(i)
			if ref, ok := parseRef(k, resp.Data[alias], aliasErrors(resp.Errors, alias)); ok {
				g.Refs[k.String()] = ref
			}
		}
	}
	if anyOK {
		g.RefsAt = now.UTC()
	}
}

func aliasErrors(errs []gqlError, alias string) []gqlError {
	var out []gqlError
	for _, e := range errs {
		if len(e.Path) > 0 && e.Path[0] == alias {
			out = append(out, e)
		}
	}
	return out
}

// parseRef interprets one alias. ok=false means "unknown, keep the previous".
func parseRef(k item.Key, raw json.RawMessage, errs []gqlError) (Ref, bool) {
	for _, e := range errs {
		if e.Type != "NOT_FOUND" {
			return Ref{}, false
		}
	}
	if len(errs) > 0 {
		return Ref{Exists: false}, true
	}
	if len(raw) == 0 || string(raw) == "null" {
		return Ref{}, false
	}
	var v struct {
		IsArchived    bool      `json:"isArchived"`
		PushedAt      time.Time `json:"pushedAt"`
		LatestRelease *struct {
			PublishedAt time.Time `json:"publishedAt"`
		} `json:"latestRelease"`
		PullRequest *struct {
			State     string    `json:"state"`
			UpdatedAt time.Time `json:"updatedAt"`
		} `json:"pullRequest"`
		Issue *struct {
			State     string    `json:"state"`
			UpdatedAt time.Time `json:"updatedAt"`
		} `json:"issue"`
		Ref *struct {
			Name string `json:"name"`
		} `json:"ref"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return Ref{}, false
	}
	switch k.Kind {
	case item.KindRepo:
		r := Ref{Exists: true, Archived: v.IsArchived, UpdatedAt: v.PushedAt}
		if v.LatestRelease != nil {
			r.LatestRelease = v.LatestRelease.PublishedAt
		}
		return r, true
	case item.KindPR:
		if v.PullRequest == nil {
			return Ref{}, false
		}
		return Ref{Exists: true, State: v.PullRequest.State, UpdatedAt: v.PullRequest.UpdatedAt}, true
	case item.KindIssue:
		if v.Issue == nil {
			return Ref{}, false
		}
		return Ref{Exists: true, State: v.Issue.State, UpdatedAt: v.Issue.UpdatedAt}, true
	case item.KindBranch:
		return Ref{Exists: v.Ref != nil}, true
	}
	return Ref{}, false
}
