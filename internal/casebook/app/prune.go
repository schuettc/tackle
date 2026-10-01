package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/schuettc/tackle/internal/casebook/config"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/journal"
	"github.com/schuettc/tackle/internal/casebook/observe"
	"github.com/schuettc/tackle/internal/casebook/temppath"
)

// PruneOptions controls PruneTemp.
type PruneOptions struct {
	Apply    bool          // rewrite and commit; false is a dry run that changes nothing
	NoPush   bool          // commit locally only
	LockWait time.Duration // how long Apply waits for casebook-data's lock (default 10s)
}

// PruneFile is one of this machine's journal day files.
type PruneFile struct {
	Path    string `json:"path"`
	Removed int    `json:"removed"`
	Kept    int    `json:"kept"`
	Delete  bool   `json:"delete,omitempty"` // every line is temp: the file goes
}

// PrefixCount is how many removed lines came from one temp-folder prefix.
type PrefixCount struct {
	Prefix string `json:"prefix"`
	Lines  int    `json:"lines"`
}

// PruneReport says what PruneTemp found, and with Apply what it did.
type PruneReport struct {
	Machine  string        `json:"machine"`
	Applied  bool          `json:"applied"`
	Files    []PruneFile   `json:"files"` // every one of this machine's journal files
	Removed  int           `json:"removed"`
	Kept     int           `json:"kept"`
	Prefixes []PrefixCount `json:"prefixes"`
	// Clones are temp clones removed from machines/<machine>.json.
	Clones []string `json:"clones"`
	// WorktreeDecisions are this machine's worktree decision files at temp
	// paths. They record intent, not observation, so prune lists them and
	// leaves them alone.
	WorktreeDecisions []string `json:"worktree_decisions"`
	Committed         bool     `json:"committed"`
	Pushed            bool     `json:"pushed"`
	Offline           bool     `json:"offline,omitempty"`
}

// prunePlan is a PruneReport plus the new contents of what changes.
type prunePlan struct {
	rep      PruneReport
	journals map[string][]byte // rel → new content (nil: delete)
	snapshot []byte            // new machines/<machine>.json, nil when unchanged
}

// PruneTemp removes the git activity in temp folders that this machine
// journalled before the hooks skipped it (temppath). Only this machine's
// journal/<machine>/ files and its machines/<machine>.json are read for
// change; every other line stays byte for byte, in order. A dry run (the
// default) changes nothing. Apply takes this machine's sync lock and then
// casebook-data's lock (giving up with ErrSyncBusy or store.ErrLocked
// without writing), rewrites each changed file atomically, re-renders the
// views when the snapshot changed, commits once and pushes. History is
// never rewritten, so the removed lines stay recoverable from git.
func (a *App) PruneTemp(ctx context.Context, o PruneOptions) (PruneReport, error) {
	if !o.Apply {
		p, err := a.planPrune()
		return p.rep, err
	}
	if o.LockWait <= 0 {
		o.LockWait = 10 * time.Second
	}
	unlock, err := LockSync()
	if err != nil {
		return PruneReport{Machine: a.Cfg.Machine}, err
	}
	defer unlock()
	// Planned before the lock to name the commit; planned again under it
	// (the sync lock keeps this machine's journal still meanwhile).
	first, err := a.planPrune()
	if err != nil {
		return first.rep, err
	}
	rep := first.rep
	rep.Applied = true
	if rep.Removed == 0 && first.snapshot == nil {
		return rep, nil
	}
	msg := fmt.Sprintf("prune %s: %d temp-folder journal events", a.Cfg.Machine, rep.Removed)
	if n := len(rep.Clones); n > 0 {
		msg += fmt.Sprintf(", %d temp clone(s)", n)
	}
	committed, err := a.Repo.BatchWithin(ctx, o.LockWait, msg, func() error {
		p, err := a.planPrune()
		if err != nil {
			return err
		}
		if p.rep.Removed != first.rep.Removed || len(p.rep.Clones) != len(first.rep.Clones) {
			return errors.New("the journal changed while prune waited for the lock; run it again")
		}
		return a.writePrune(p)
	})
	if err != nil {
		return rep, err
	}
	rep.Committed = committed
	if o.NoPush || !committed {
		return rep, nil
	}
	var sr SyncReport
	_, err = a.pushSync(ctx, &sr)
	rep.Pushed, rep.Offline = sr.Pushed, sr.Offline
	return rep, err
}

// writePrune writes a plan; it runs under casebook-data's lock.
func (a *App) writePrune(p prunePlan) error {
	for rel, b := range p.journals {
		if b == nil {
			if err := os.Remove(filepath.Join(a.Repo.Dir, filepath.FromSlash(rel))); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if _, err := a.Repo.WriteFile(rel, b); err != nil {
			return err
		}
	}
	if p.snapshot == nil {
		return nil
	}
	if _, err := a.Repo.WriteFile("machines/"+a.Cfg.Machine+".json", p.snapshot); err != nil {
		return err
	}
	// The views list the clones: render them from the cleaned snapshots.
	snaps, err := a.snapshots()
	if err != nil {
		return err
	}
	g, err := observe.LoadGitHub(config.CachePath())
	if err != nil {
		return err
	}
	var sr SyncReport
	return a.render(g, snaps, &sr)
}

// planPrune reads this machine's journal and snapshot and works out what
// prune removes. It writes nothing.
func (a *App) planPrune() (prunePlan, error) {
	m := a.Cfg.Machine
	p := prunePlan{rep: PruneReport{Machine: m, Files: []PruneFile{}, Prefixes: []PrefixCount{}, Clones: []string{}, WorktreeDecisions: []string{}},
		journals: map[string][]byte{}}
	if m == "" || strings.ContainsAny(m, `/\*?[`) {
		return p, fmt.Errorf("machine name %q can't name a journal directory", m)
	}
	temp := temppath.New(a.Cfg.Roots)
	files, err := a.Repo.Glob("journal/" + m + "/*/*.jsonl")
	if err != nil {
		return p, err
	}
	prefixes := map[string]int{}
	for _, rel := range files {
		b, err := a.Repo.ReadFile(rel)
		if err != nil {
			return p, err
		}
		out, removed, kept := pruneLines(b, temp, prefixes)
		pf := PruneFile{Path: rel, Removed: removed, Kept: kept, Delete: removed > 0 && len(out) == 0}
		p.rep.Files = append(p.rep.Files, pf)
		p.rep.Removed += removed
		p.rep.Kept += kept
		switch {
		case removed == 0:
		case pf.Delete:
			p.journals[rel] = nil
		default:
			p.journals[rel] = out
		}
	}
	for pre, n := range prefixes {
		p.rep.Prefixes = append(p.rep.Prefixes, PrefixCount{Prefix: pre, Lines: n})
	}
	sort.Slice(p.rep.Prefixes, func(i, j int) bool {
		x, y := p.rep.Prefixes[i], p.rep.Prefixes[j]
		return x.Lines > y.Lines || x.Lines == y.Lines && x.Prefix < y.Prefix
	})

	snap, ok, err := a.MachineSnapshot()
	if err != nil {
		return p, err
	}
	if ok {
		kept := snap.Clones[:0:0]
		for _, c := range snap.Clones {
			if temp.Path(c.Path) {
				p.rep.Clones = append(p.rep.Clones, c.Path)
				continue
			}
			kept = append(kept, c)
		}
		if len(p.rep.Clones) > 0 {
			snap.Clones = kept
			if p.snapshot, err = observe.EncodeSnapshot(snap); err != nil {
				return p, err
			}
		}
	}

	decs, err := a.Repo.Glob("items/worktree/" + m + "/*.toml")
	if err != nil {
		return p, err
	}
	for _, rel := range decs {
		if k, err := item.KeyFromFile(rel); err == nil && temp.Path(k.Path) {
			p.rep.WorktreeDecisions = append(p.rep.WorktreeDecisions, rel)
		}
	}
	return p, nil
}

// pruneLines drops the lines of one journal file that are temp events and
// returns the rest byte for byte, in order, each with its own terminator.
// A line that isn't a journal event is kept. prefixes counts each removed
// line's temp-folder prefix.
func pruneLines(b []byte, temp *temppath.Matcher, prefixes map[string]int) (out []byte, removed, kept int) {
	out = make([]byte, 0, len(b))
	for len(b) > 0 {
		line := b
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			line = b[:i+1]
		}
		b = b[len(line):]
		var ev journal.Event
		if json.Unmarshal(bytes.TrimSuffix(line, []byte("\n")), &ev) == nil {
			if where := temp.Where(ev); where != "" {
				removed++
				prefixes[tempPrefix(temp, where)]++
				continue
			}
		}
		kept++
		out = append(out, line...)
	}
	return out, removed, kept
}

// tempPrefix shortens a temp path to the folder that names its source, for
// the report: the temp root plus its first segment (macOS's
// /var/folders/<xx>/<id>/<T> counts as the root), with the segment's random
// or numeric tail as "*", and /private/tmp and /private/var shown as /tmp
// and /var so both spellings count together:
// /private/var/folders/92/ab/T/casebook-probe-48603/data → /var/folders/92/ab/T/casebook-probe-*.
func tempPrefix(temp *temppath.Matcher, p string) string {
	root, matched := temp.Root(p)
	if root == "" {
		return p
	}
	rest := strings.Split(strings.TrimPrefix(strings.TrimPrefix(matched, root), "/"), "/")
	out := root
	if rest[0] != "" {
		n := 1
		if strings.HasSuffix(root, "/var/folders") {
			n = 4
		}
		n = min(n, len(rest))
		rest = rest[:n]
		rest[n-1] = generalize(rest[n-1])
		out = root + "/" + strings.Join(rest, "/")
	}
	for _, pre := range []string{"/private/tmp", "/private/var"} {
		if out == pre || strings.HasPrefix(out, pre+"/") {
			return strings.TrimPrefix(out, "/private")
		}
	}
	return out
}

// generalize cuts a folder name at its first random-looking token (split on
// '.', '-' and '_'): one with a digit, a later one of one or two characters,
// or a later mixed-case one; the cut becomes "*". A first token keeps its
// letters before the first digit. casebook-probe-48603-x → casebook-probe-*,
// tmp1jyhsb7j → tmp*, tmp.AbCdEf → tmp.*, copier._vcs.clone.v_k3j2 →
// copier._vcs.clone.*, TestFoo2_bad → TestFoo*, pytest-of-court stays.
func generalize(seg string) string {
	first := true
	for start := 0; start < len(seg); {
		end := strings.IndexAny(seg[start:], ".-_")
		if end < 0 {
			end = len(seg)
		} else {
			end += start
		}
		tok := seg[start:end]
		if tok != "" {
			var digit, upper, lower bool
			for _, r := range tok {
				digit = digit || unicode.IsDigit(r)
				upper = upper || unicode.IsUpper(r)
				lower = lower || unicode.IsLower(r)
			}
			switch {
			case first && digit:
				return seg[:start+strings.IndexFunc(tok, unicode.IsDigit)] + "*"
			case !first && (digit || len(tok) == 1 || upper && lower):
				return seg[:start] + "*"
			}
			first = false
		}
		start = end + 1
	}
	return seg
}
