package check

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/schuettc/tackle/internal/sift/discover"
	"github.com/schuettc/tackle/internal/sift/profile"
	"github.com/schuettc/tackle/internal/sift/row"
)

var (
	backtickRE = regexp.MustCompile("`([^`\\s]+)`")
	linkRE     = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	segmentRE  = regexp.MustCompile(`^[\w.+-]*$`)
	extRE      = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)
	lineSufRE  = regexp.MustCompile(`:\d+(?:-\d+)?$`)
)

// candidate returns the path a backticked span or link target names, or ""
// when it does not look like a path: it needs a slash, plain segments, and
// a file extension, a trailing slash or a ./, ../, ~/ or / start. URLs,
// placeholders, globs, ellipses, slash commands (/ship) and host-like first
// segments (example.tools/x) are not paths.
func candidate(s string) string {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "mailto:") || strings.HasPrefix(s, "#") {
		return ""
	}
	s = strings.SplitN(s, "#", 2)[0]
	s = lineSufRE.ReplaceAllString(s, "")
	s = strings.TrimRight(s, ".,;:")
	if !strings.Contains(s, "/") {
		return ""
	}
	anchored := strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, "/")
	segs := strings.Split(strings.TrimPrefix(strings.TrimPrefix(s, "~/"), "/"), "/")
	for _, seg := range segs {
		if !segmentRE.MatchString(seg) || strings.HasPrefix(seg, "...") {
			return ""
		}
	}
	if strings.HasPrefix(s, "/") && len(segs) == 1 && !extRE.MatchString(segs[0]) {
		return "" // a slash command
	}
	if !anchored && strings.Contains(segs[0], ".") && !strings.HasPrefix(segs[0], ".") {
		return "" // a host, not a directory
	}
	if !anchored && !strings.HasSuffix(s, "/") && !extRE.MatchString(segs[len(segs)-1]) {
		return ""
	}
	return s
}

// deadPath flags backticked paths and link targets that do not resolve. In
// a repo file a relative path is resolved from the repo root and from the
// file's directory, at the audited commit and then on disk (an untracked file
// or a nested repo is alive), and then in the other audited repos (the line
// may name a sibling's file). A miss is certain only when the repo's history
// had the path as a file and deleted it; anything else (a directory, an
// extensionless convention, a file still to write) is a judgment. Home and
// absolute paths are checked on disk, and a miss there is a judgment: the
// path may exist on another machine. Elsewhere only those are checked: a
// skill is used in whatever repo the agent is in.
func deadPath(_ context.Context, in *Input) []row.Row {
	var rows []row.Row
	for _, f := range in.Files {
		for _, l := range prose(f.Content) {
			var missing []row.Fact
			certain := true
			seen := map[string]bool{}
			var spans []string
			for _, m := range backtickRE.FindAllStringSubmatch(l.Text, -1) {
				spans = append(spans, m[1])
			}
			for _, m := range linkRE.FindAllStringSubmatch(l.Text, -1) {
				spans = append(spans, m[1])
			}
			for _, s := range spans {
				p := candidate(s)
				if p == "" || seen[p] {
					continue
				}
				seen[p] = true
				alive, sure, checked := resolve(f, p, in.Repos)
				if !checked || alive {
					continue
				}
				missing = append(missing, fact("missing", "%s", p))
				certain = certain && sure
			}
			if len(missing) == 0 {
				continue
			}
			summary := "a path here does not exist"
			if len(missing) > 1 {
				summary = "paths here do not exist"
			}
			rows = append(rows, newRow(f, "dead-path", l.N, l.N, l.Text, "", summary, certain, missing...))
		}
	}
	return rows
}

// resolve reports whether p exists, whether a miss is certain, and whether
// p was checked at all.
func resolve(f *discover.File, p string, repos []*discover.Repo) (alive, certain, checked bool) {
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "/") {
		_, err := os.Stat(profile.Expand(p))
		return err == nil, false, true
	}
	if f.Class == discover.ClassSkill {
		return false, false, false
	}
	if f.Repo == nil {
		if !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../") {
			return false, false, false
		}
		_, err := os.Stat(filepath.Join(filepath.Dir(f.Path), p))
		return err == nil, false, true
	}
	var rels []string
	if dir := path.Dir(f.Rel); dir != "." {
		rels = append(rels, path.Join(dir, p))
	}
	rels = append(rels, path.Clean(p))
	gone := false
	for _, rel := range rels {
		if rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		if f.Repo.Has(rel) {
			return true, false, true
		}
		gone = gone || f.Repo.Gone[rel]
	}
	for _, rel := range rels {
		if _, err := os.Stat(filepath.Join(f.Repo.Root, filepath.FromSlash(rel))); err == nil {
			return true, false, true
		}
	}
	for _, r := range repos {
		if r != f.Repo && r.Has(p) {
			return true, false, true
		}
	}
	return false, gone && !strings.HasSuffix(p, "/"), true
}
