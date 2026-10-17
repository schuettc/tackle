package ops

import (
	"context"
	"fmt"
	"path"
	"strings"
)

// Trust is one trusted publisher as npm trust list prints it.
type Trust struct {
	ID, Type, File, Repository string
}

// ParseTrust reads npm trust list's plain-text blocks of "key: value"
// lines. A block ends at a blank line or where a key repeats.
func ParseTrust(output string) []Trust {
	var out []Trust
	var cur Trust
	seen := map[string]bool{}
	flush := func() {
		if cur.ID != "" || cur.Type != "" {
			out = append(out, cur)
		}
		cur, seen = Trust{}, map[string]bool{}
	}
	for l := range strings.SplitSeq(output, "\n") {
		l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- "))
		if l == "" {
			flush()
			continue
		}
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		var field *string
		switch k {
		case "id":
			field = &cur.ID
		case "type":
			field = &cur.Type
		case "file":
			field = &cur.File
		case "repository":
			field = &cur.Repository
		default:
			continue
		}
		if seen[k] {
			flush()
		}
		seen[k] = true
		*field = v
	}
	flush()
	return out
}

// matches reports whether t is the GitHub trusted publisher for repo and
// workflow (a file name; a path is read as its base name).
func (t Trust) matches(repo, workflow string) bool {
	return (t.Type == "" || strings.EqualFold(t.Type, "github")) &&
		strings.EqualFold(t.Repository, repo) && t.File == path.Base(workflow)
}

func (o *Ops) trustList(ctx context.Context, pkg string, report Report) ([]Trust, error) {
	res, err := o.npm(ctx, "trust", pkg, []string{"trust", "list", pkg}, report)
	if err != nil {
		return nil, err
	}
	return ParseTrust(res.Output), nil
}

// Trust makes repo's workflow the package's one trusted publisher:
// nothing when it already is; otherwise each existing one is revoked, the
// new one created and read back.
func (o *Ops) Trust(ctx context.Context, pkg, repo, workflow string, report Report) error {
	workflow = path.Base(workflow)
	p, err := o.Reg.Package(ctx, pkg)
	if err != nil {
		return fail(report, fmt.Errorf("failed: reading the registry: %w", err))
	}
	if !p.Exists {
		return fail(report, fmt.Errorf("%s isn't on npm yet, and a trusted publisher can only be set on a package that exists; publish its first version with imprint_publish", pkg))
	}
	have, err := o.trustList(ctx, pkg, report)
	if err != nil {
		return fail(report, err)
	}
	for _, t := range have {
		if t.matches(repo, workflow) {
			report(fmt.Sprintf("%s already trusts %s (%s); nothing to change", pkg, repo, workflow))
			return nil
		}
	}
	for _, t := range have {
		if t.ID == "" {
			return fail(report, fmt.Errorf("failed: npm trust list shows a trusted publisher of %s with no id (%s %s); revoke it at npmjs.com", pkg, t.Repository, t.File))
		}
		if _, err := o.npm(ctx, "trust", pkg, []string{"trust", "revoke", pkg, "--id=" + t.ID}, report); err != nil {
			return fail(report, err)
		}
		report(fmt.Sprintf("revoked the trusted publisher %s (%s)", t.Repository, t.File))
	}
	if _, err := o.npm(ctx, "trust", pkg, []string{"trust", "github", pkg, "--file", workflow, "--repo", repo, "--allow-publish", "-y"}, report); err != nil {
		return fail(report, err)
	}
	now, err := o.trustList(ctx, pkg, report)
	if err != nil {
		return fail(report, err)
	}
	found := false
	for _, t := range now {
		if t.matches(repo, workflow) {
			found = true
		}
	}
	if !found {
		return fail(report, fmt.Errorf("failed: npm trust github returned, but npm trust list doesn't show %s (%s) for %s; check it at npmjs.com", repo, workflow, pkg))
	}
	report(fmt.Sprintf("%s now trusts %s (%s). This trusted publisher is unproven: run one CI release and give me the version (imprint_expect)", pkg, repo, workflow))
	return nil
}
