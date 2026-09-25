// Package observe gathers what the ledger knows about the world: GitHub
// (repos, pull requests, issues) and this machine's clones, branches and
// worktrees.
package observe

import "time"

// CacheVersion and SnapshotVersion are the on-disk formats this binary writes.
const (
	CacheVersion    = 1
	SnapshotVersion = 1
)

// RepoObs is one repository as GitHub reported it.
type RepoObs struct {
	Repo            string    `json:"repo"` // Owner/Name as GitHub spells it
	Archived        bool      `json:"archived,omitempty"`
	Fork            bool      `json:"fork,omitempty"`
	Private         bool      `json:"private,omitempty"`
	PushedAt        time.Time `json:"pushed_at,omitzero"`
	Parent          string    `json:"parent,omitempty"`
	DefaultBranch   string    `json:"default_branch,omitempty"`
	LatestRelease   time.Time `json:"latest_release,omitzero"`
	PRs             []PRObs   `json:"prs,omitempty"`    // open
	Issues          []PRObs   `json:"issues,omitempty"` // open
	PRsTruncated    bool      `json:"prs_truncated,omitempty"`
	IssuesTruncated bool      `json:"issues_truncated,omitempty"`
}

// PRObs is one pull request or issue.
type PRObs struct {
	Repo              string    `json:"repo"`
	Number            int       `json:"number"`
	Title             string    `json:"title,omitempty"`
	Author            string    `json:"author,omitempty"`
	State             string    `json:"state,omitempty"` // OPEN, CLOSED, MERGED
	Draft             bool      `json:"draft,omitempty"`
	CreatedAt         time.Time `json:"created_at,omitzero"`
	UpdatedAt         time.Time `json:"updated_at,omitzero"`
	LastCommentAuthor string    `json:"last_comment_author,omitempty"`
	LastCommentAt     time.Time `json:"last_comment_at,omitzero"`
	URL               string    `json:"url,omitempty"`
}

// Owner is one account's or org's observation. A failed or blocked fetch
// keeps the previous Repos and says so (absence is not evidence).
type Owner struct {
	Login     string    `json:"login"`
	Reachable bool      `json:"reachable"`
	Reason    string    `json:"reason,omitempty"` // why unreachable, or the last fetch error
	Stale     bool      `json:"stale,omitempty"`  // Repos are from FetchedAt, not the latest attempt
	FetchedAt time.Time `json:"fetched_at,omitzero"`
	Repos     []RepoObs `json:"repos,omitempty"`
}

// Ref is the looked-up state of one item referenced by a decision or an
// until condition that the owner sweep doesn't cover.
type Ref struct {
	Exists        bool      `json:"exists"`
	State         string    `json:"state,omitempty"`    // pr/issue
	Archived      bool      `json:"archived,omitempty"` // repo
	LatestRelease time.Time `json:"latest_release,omitzero"`
	UpdatedAt     time.Time `json:"updated_at,omitzero"`
}

// GitHub is the machine-local cache of everything fetched from GitHub.
type GitHub struct {
	Version    int               `json:"version"`
	User       string            `json:"user,omitempty"`
	Owners     map[string]*Owner `json:"owners"` // key: lower-case login
	Authored   []PRObs           `json:"authored,omitempty"`
	AuthoredAt time.Time         `json:"authored_at,omitzero"`
	Refs       map[string]Ref    `json:"refs"` // key: item key text
	RefsAt     time.Time         `json:"refs_at,omitzero"`
}

// Snapshot is one machine's local git state (machines/<machine>.json in the
// ledger repo). It holds no timestamps of its own, so an unchanged machine
// produces an unchanged file.
type Snapshot struct {
	Version int      `json:"version"`
	Machine string   `json:"machine"`
	Roots   []string `json:"roots"`
	Clones  []Clone  `json:"clones"`
}

// Clone is one git clone (or bare repository) found under a root.
type Clone struct {
	Path           string            `json:"path"`
	Repo           string            `json:"repo,omitempty"`    // lower-case owner/name of the identifying GitHub remote
	Remotes        map[string]string `json:"remotes,omitempty"` // name → owner/name, or "url:<redacted url>" for non-GitHub
	Bare           bool              `json:"bare,omitempty"`
	Dirty          bool              `json:"dirty,omitempty"`
	Stashes        int               `json:"stashes,omitempty"`
	LocalHooksPath string            `json:"local_hooks_path,omitempty"` // set: global hooks don't run here
	Branches       []Branch          `json:"branches,omitempty"`
	Worktrees      []Worktree        `json:"worktrees,omitempty"`
}

// Branch is one local branch.
type Branch struct {
	Name           string    `json:"name"`
	Upstream       string    `json:"upstream,omitempty"`
	Ahead          int       `json:"ahead,omitempty"`
	Gone           bool      `json:"gone,omitempty"`     // upstream set but deleted on the remote (typically merged)
	Unpushed       int       `json:"unpushed,omitempty"` // commits on no remote
	OldestUnpushed time.Time `json:"oldest_unpushed,omitzero"`
	Tip            string    `json:"tip"`
}

// Worktree is one linked worktree of a clone.
type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Head     string `json:"head,omitempty"`
	Detached bool   `json:"detached,omitempty"`
	Dirty    bool   `json:"dirty,omitempty"`
}
