package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/verify"
)

// DefaultTimeout is the per-command test timeout when Options.Timeout is 0.
const DefaultTimeout = 15 * time.Minute

// Options configures one `cull apply` run.
type Options struct {
	Root        string        // project root (holds .cull/last.json)
	IDs         []string      // remove exactly these tests (exclusive with VerdictCut)
	VerdictCut  bool          // remove every top-level test judged cut
	NoVerify    bool          // skip the baseline and after test runs
	Timeout     time.Duration // per test command; 0 means DefaultTimeout
	TestCommand string        // .cull.toml test_command ("" = auto-detect)
	Stderr      io.Writer     // progress lines; nil discards
	Notice      io.Writer     // the snapshot path line; nil means Stderr
}

// Outcome is what one apply run did (or would have done, when RolledBack).
// Applied, Files, ImportsRemoved and OrphanedHelpers describe the edit that
// was written; when RolledBack is true that edit has since been undone and
// every file is byte-for-byte as it was. RollbackFailed means a restore was
// attempted and did not fully succeed: the files may still be edited, and
// the pre-edit copies are in Snapshot.
type Outcome struct {
	Applied         []string            `json:"applied"`
	Refused         []Refusal           `json:"refused"`
	NeedsAgent      []Refusal           `json:"needs_agent"`
	Files           []string            `json:"files"`
	ImportsRemoved  map[string][]string `json:"imports_removed"`
	OrphanedHelpers map[string][]string `json:"orphaned_helpers"`
	Baseline        []verify.Result     `json:"baseline"`
	After           []verify.Result     `json:"after"`
	RolledBack      bool                `json:"rolled_back"`
	RollbackFailed  bool                `json:"rollback_failed"`
	Snapshot        string              `json:"snapshot,omitempty"`
}

// ExitError is a Run failure with the process exit code it maps to:
// 1 = the after-run failed (or timed out) and every file was restored;
// 2 = refused or error (nothing changed, or -- if a write had happened --
// restored, or the restore failed), including a test command that could
// not start after the edit.
type ExitError struct {
	Code int
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

func exitf(code int, format string, a ...any) *ExitError {
	return &ExitError{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// runVerify and writeFile are swapped by tests to inject after-only test
// failures and write/restore failures.
var (
	runVerify = verify.Run
	writeFile = writeFileMode
)

// writeFileMode overwrites path with data and (re)sets its permission bits
// to mode, so an edit or a restore never changes a file's permissions.
func writeFileMode(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// fileEdit is one file apply will rewrite: its pre-edit bytes, mode and
// sha256 (the in-memory snapshot restore works from), and the new bytes.
type fileEdit struct {
	rel     string
	abs     string
	lang    string
	orig    []byte
	mode    os.FileMode
	sum     string
	spans   []cases.Span
	updated []byte

	importsRemoved []string
	orphans        []string
}

func fileSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Run removes the selected tests. Nothing is written until selection,
// preflight, the test plan and the baseline run have all succeeded; the
// first write is preceded by a snapshot (on disk under
// <root>/.cull/rollback/<UTC time>/ and in memory). A failing after-run,
// or any error once a write has happened, restores every file from the
// in-memory copy and checks each file's sha256 against its pre-edit value.
// The returned error, if any, is an *ExitError.
func Run(ctx context.Context, opt Options) (out Outcome, err error) {
	if (len(opt.IDs) > 0) == opt.VerdictCut {
		return out, exitf(2, "pass exactly one of --ids or --verdict cut")
	}
	stderr := opt.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	root := opt.Root

	report, lerr := Load(root)
	if lerr != nil {
		return out, exitf(2, "%v", lerr)
	}

	// Select, then preflight. With --ids any refusal stops everything;
	// with --verdict cut refused tests are listed and the rest proceed.
	targets, refused, needsAgent := Select(report, opt.IDs, opt.VerdictCut)
	out.Refused, out.NeedsAgent = refused, needsAgent
	targets = dedupeTargets(targets)
	if !opt.VerdictCut && len(refused) > 0 {
		return out, exitf(2, "refused %d test id(s); nothing changed", len(refused))
	}
	if pre := Preflight(root, report, targets); len(pre) > 0 {
		out.Refused = append(out.Refused, pre...)
		if !opt.VerdictCut {
			return out, exitf(2, "refused %d test id(s); nothing changed", len(pre))
		}
		targets = dropRefused(targets, pre)
	}
	// A file the cut would empty is left alone (--ids: refused).
	targets, held := HoldEmptiedFiles(report, targets)
	if len(held) > 0 {
		if !opt.VerdictCut {
			out.Refused = append(out.Refused, held...)
			return out, exitf(2, "refused %d test id(s); nothing changed", len(held))
		}
		out.NeedsAgent = append(out.NeedsAgent, held...)
	}
	if len(targets) == 0 {
		return out, nil
	}

	edits, perr := planEdits(root, targets)
	if perr != nil {
		return out, exitf(2, "%v; nothing changed", perr)
	}
	files := map[string]string{}
	for _, e := range edits {
		files[e.rel] = e.lang
	}

	// Baseline: the scoped tests must pass before anything is touched.
	var cmds []verify.Command
	if !opt.NoVerify {
		cmds, err = verify.Plan(root, opt.TestCommand, files)
		if err != nil {
			return out, exitf(2, "%v", err)
		}
		logCommands(stderr, "baseline", cmds)
		out.Baseline = runVerify(ctx, cmds, timeout)
		if !allOK(out.Baseline, cmds) {
			if ctx.Err() != nil {
				return out, exitf(2, "interrupted during the baseline test run; nothing changed")
			}
			if r, ok := startFailure(out.Baseline); ok {
				return out, exitf(2, "test command could not start (%s: %s); nothing changed",
					r.Command, strings.TrimSpace(r.OutputTail))
			}
			return out, exitf(2, "tests already fail before any change; nothing changed")
		}
	}

	// Read the pre-edit bytes (the in-memory snapshot) and compute every
	// edit before writing anything. The file must still hash to what
	// check recorded: the baseline run could have touched it.
	for i := range edits {
		if err := loadAndEdit(root, report.Files[edits[i].rel].SHA256, &edits[i]); err != nil {
			return out, exitf(2, "%v; nothing changed", err)
		}
	}
	for _, t := range targets {
		out.Applied = append(out.Applied, t.ID)
	}
	out.ImportsRemoved = map[string][]string{}
	out.OrphanedHelpers = map[string][]string{}
	for _, e := range edits {
		out.Files = append(out.Files, e.rel)
		if len(e.importsRemoved) > 0 {
			out.ImportsRemoved[e.rel] = e.importsRemoved
		}
		if len(e.orphans) > 0 {
			out.OrphanedHelpers[e.rel] = e.orphans
		}
	}

	snap, serr := writeSnapshot(root, edits)
	if serr != nil {
		return out, exitf(2, "writing rollback snapshot: %v; nothing changed", serr)
	}
	out.Snapshot = snap
	notice := opt.Notice
	if notice == nil {
		notice = stderr
	}
	_, _ = fmt.Fprintf(notice, "cull apply: snapshot %s\n", snap)

	// An interrupt that arrived after the baseline: nothing is written yet.
	if ctx.Err() != nil {
		return out, exitf(2, "interrupted before any change; nothing changed")
	}

	// From the first write on, every exit path restores.
	wrote := false
	defer func() {
		if p := recover(); p != nil {
			if wrote {
				_ = restore(edits)
			}
			panic(p)
		}
	}()
	for _, e := range edits {
		wrote = true
		if werr := writeFile(e.abs, e.updated, e.mode); werr != nil {
			if rerr := restore(edits); rerr != nil {
				out.RollbackFailed = true
				return out, restoreFailed(snap, rerr)
			}
			out.RolledBack = true
			return out, exitf(2, "writing %s: %v; rolled back (snapshot: %s)", e.rel, werr, snap)
		}
	}

	if opt.NoVerify {
		if ctx.Err() == nil {
			return out, nil
		}
		if rerr := restore(edits); rerr != nil {
			out.RollbackFailed = true
			return out, restoreFailed(snap, rerr)
		}
		out.RolledBack = true
		return out, exitf(2, "interrupted; rolled back (snapshot: %s)", snap)
	}
	logCommands(stderr, "after", cmds)
	out.After = runVerify(ctx, cmds, timeout)
	if allOK(out.After, cmds) {
		return out, nil
	}
	if rerr := restore(edits); rerr != nil {
		out.RollbackFailed = true
		return out, restoreFailed(snap, rerr)
	}
	out.RolledBack = true
	if ctx.Err() != nil {
		return out, exitf(2, "interrupted during the test run; rolled back (snapshot: %s)", snap)
	}
	if r, ok := startFailure(out.After); ok {
		return out, exitf(2, "test command could not start after removal (%s: %s); rolled back (snapshot: %s)",
			r.Command, strings.TrimSpace(r.OutputTail), snap)
	}
	return out, exitf(1, "tests failed after removal (%s); rolled back (snapshot: %s)%s", failedCommand(out.After), snap, goimportsHint(edits))
}

// goimportsHint is appended to a failed after-run when a Go file was
// tidied without goimports: an import the fallback had to keep may be
// the cause.
func goimportsHint(edits []fileEdit) string {
	for _, e := range edits {
		if e.lang == "go" && goTidyFallsBack() {
			return "; goimports is not on PATH, so unused imports may remain -- install it (go install golang.org/x/tools/cmd/goimports@latest) and retry"
		}
	}
	return ""
}

// dedupeTargets drops repeated ids (e.g. `--ids X --ids X`): removing the
// same span twice would delete unrelated bytes.
func dedupeTargets(ts []Target) []Target {
	seen := map[string]bool{}
	var out []Target
	for _, t := range ts {
		if seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		out = append(out, t)
	}
	return out
}

func dropRefused(ts []Target, refused []Refusal) []Target {
	bad := map[string]bool{}
	for _, r := range refused {
		bad[r.ID] = true
	}
	var out []Target
	for _, t := range ts {
		if !bad[t.ID] {
			out = append(out, t)
		}
	}
	return out
}

// planEdits groups targets by file (sorted by relpath). A relpath that
// escapes root, or spans that overlap within a file, is an error.
func planEdits(root string, ts []Target) ([]fileEdit, error) {
	byFile := map[string]*fileEdit{}
	var rels []string
	for _, t := range ts {
		e, ok := byFile[t.File]
		if !ok {
			native := filepath.FromSlash(t.File)
			if !filepath.IsLocal(native) {
				return nil, fmt.Errorf("%s: path is outside the project root", t.File)
			}
			e = &fileEdit{rel: t.File, abs: filepath.Join(root, native), lang: t.Lang}
			byFile[t.File] = e
			rels = append(rels, t.File)
		}
		e.spans = append(e.spans, t.Span)
	}
	sort.Strings(rels)
	edits := make([]fileEdit, 0, len(rels))
	for _, rel := range rels {
		e := byFile[rel]
		sort.Slice(e.spans, func(i, j int) bool { return e.spans[i].Start < e.spans[j].Start })
		for i := 1; i < len(e.spans); i++ {
			if e.spans[i].Start < e.spans[i-1].End {
				return nil, fmt.Errorf("%s: selected tests overlap", rel)
			}
		}
		edits = append(edits, *e)
	}
	return edits, nil
}

// loadAndEdit reads e's file (bytes + mode), checks it still hashes to
// wantSum, and computes the edited bytes: spans removed, imports tidied,
// orphaned helpers noted. It writes nothing.
func loadAndEdit(root, wantSum string, e *fileEdit) error {
	fi, err := os.Stat(e.abs)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", e.rel)
	}
	data, err := os.ReadFile(e.abs)
	if err != nil {
		return err
	}
	e.orig, e.mode, e.sum = data, fi.Mode().Perm(), fileSum(data)
	if e.sum != wantSum {
		return fmt.Errorf("%s changed since cull check (during the baseline run?); run cull check again", e.rel)
	}
	bodies := make([]string, 0, len(e.spans))
	for _, sp := range e.spans {
		if sp.Start < 0 || sp.End > len(data) || sp.Start > sp.End {
			return fmt.Errorf("%s: span out of range", e.rel)
		}
		bodies = append(bodies, string(data[sp.Start:sp.End]))
	}
	spans := e.spans
	if e.lang == "typescript" {
		spans = withTSSemicolons(data, spans)
	}
	removed := RemoveSpans(data, spans)
	tidied, imports, err := Tidy(root, e.rel, e.lang, data, removed)
	if err != nil {
		return fmt.Errorf("tidying %s: %w", e.rel, err)
	}
	e.updated = tidied
	e.importsRemoved = imports
	e.orphans = OrphanedHelpers(data, tidied, e.lang, bodies)
	return nil
}

// writeSnapshot copies every file's pre-edit bytes to
// <root>/.cull/rollback/<UTC yyyymmddThhmmssZ>/<relpath> (dirs 0700,
// files 0600) and returns that directory. On error it removes the partial
// snapshot directory it created.
func writeSnapshot(root string, edits []fileEdit) (string, error) {
	base := filepath.Join(root, ".cull", "rollback")
	if err := mkdir700(base); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dir := filepath.Join(base, stamp)
	for n := 1; ; n++ {
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			break
		}
		if !os.IsExist(err) || n > 100 {
			return "", err
		}
		dir = filepath.Join(base, fmt.Sprintf("%s-%d", stamp, n))
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	for _, e := range edits {
		if err := snapshotFile(dir, e); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// mkdir700 creates dir and any missing parents, each with mode 0700
// (forced, regardless of umask). Existing directories are left as-is,
// except dir itself, which is set to 0700.
func mkdir700(dir string) error {
	if fi, err := os.Stat(dir); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
		return os.Chmod(dir, 0o700)
	}
	if err := mkdir700Parents(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func mkdir700Parents(dir string) error {
	if _, err := os.Stat(dir); err == nil {
		return nil
	}
	if err := mkdir700Parents(filepath.Dir(dir)); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func snapshotFile(dir string, e fileEdit) error {
	dst := filepath.Join(dir, filepath.FromSlash(e.rel))
	if err := mkdir700Parents(filepath.Dir(dst)); err != nil {
		return err
	}
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(e.orig); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, 0o600)
}

// restore puts every file back from its in-memory pre-edit copy and
// checks the result hashes to the pre-edit sha256. A file already intact
// is not rewritten (only its mode is reset). It tries every file even if
// one fails, and reports all failures.
func restore(edits []fileEdit) error {
	var problems []string
	for _, e := range edits {
		if cur, err := os.ReadFile(e.abs); err == nil && fileSum(cur) == e.sum {
			if fi, err := os.Stat(e.abs); err == nil && fi.Mode().Perm() != e.mode {
				_ = os.Chmod(e.abs, e.mode)
			}
			continue
		}
		if err := writeFile(e.abs, e.orig, e.mode); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.rel, err))
			continue
		}
		cur, err := os.ReadFile(e.abs)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", e.rel, err))
			continue
		}
		if fileSum(cur) != e.sum {
			problems = append(problems, fmt.Sprintf("%s: sha256 after restore does not match the pre-edit file", e.rel))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func restoreFailed(snap string, err error) *ExitError {
	return exitf(2, "ROLLBACK FAILED: %v; the pre-edit files are in %s -- copy them back by hand", err, snap)
}

// allOK reports whether every planned command ran and passed (Run stops
// at the first failure, so fewer results than commands is a failure).
func allOK(rs []verify.Result, cmds []verify.Command) bool {
	if len(rs) < len(cmds) {
		return false
	}
	for _, r := range rs {
		if !r.OK {
			return false
		}
	}
	return true
}

// startFailure returns the first result whose command failed to start
// (exit -1 without a timeout): an environment problem, not a test failure.
func startFailure(rs []verify.Result) (verify.Result, bool) {
	for _, r := range rs {
		if !r.OK && r.ExitCode == -1 && !r.TimedOut {
			return r, true
		}
	}
	return verify.Result{}, false
}

func failedCommand(rs []verify.Result) string {
	for _, r := range rs {
		if !r.OK {
			if r.TimedOut {
				return r.Command + ": timed out"
			}
			return fmt.Sprintf("%s: exit %d", r.Command, r.ExitCode)
		}
	}
	return "incomplete test run"
}

func logCommands(w io.Writer, phase string, cmds []verify.Command) {
	for _, c := range cmds {
		label := c.Shell
		if label == "" {
			label = strings.Join(c.Argv, " ")
		}
		_, _ = fmt.Fprintf(w, "cull apply: %s tests: %s\n", phase, label)
	}
}
