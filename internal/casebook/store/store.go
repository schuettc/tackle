// Package store is the casebook data repository: a git repo that holds decisions
// (items/), journals (journal/), machine snapshots (machines/), views and
// shared policy. The casebook binary is its only writer; history is linear.
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/rules"
	tools "github.com/schuettc/tools-common"
)

// FormatVersion is the casebook repo format this binary reads and writes
// (casebook.toml format_version). Documented in internal/casebook/FORMAT.md.
const FormatVersion = 2

const readme = "# casebook\n\nThis repository is written by `casebook sync`. Views appear here after the first sync.\n"

// Repo is an open casebook data repository.
type Repo struct {
	Dir string
	// Version is the format_version read from casebook.toml when opened; Upgrade
	// raises it to FormatVersion.
	Version int
}

type meta struct {
	FormatVersion int `toml:"format_version"`
}

// Init prepares dir as a casebook clone of remote. An existing clone at dir is
// opened. An empty remote is bootstrapped (casebook.toml, policy.toml, README.md)
// and pushed.
func Init(ctx context.Context, dir, remote string) (*Repo, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return Open(dir)
	}
	if err := tools.EnsureDir(filepath.Dir(dir)); err != nil {
		return nil, err
	}
	if _, err := gitx.Run(ctx, filepath.Dir(dir), "clone", "-q", remote, dir); err != nil {
		return nil, fmt.Errorf("clone casebook repo: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "casebook.toml")); err == nil {
		return Open(dir)
	}
	if _, err := gitx.Run(ctx, dir, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		return nil, err
	}
	r := &Repo{Dir: dir, Version: FormatVersion}
	pol, err := item.EncodePolicy(item.DefaultPolicy())
	if err != nil {
		return nil, err
	}
	for rel, b := range map[string][]byte{
		"casebook.toml": fmt.Appendf(nil, "# casebook data repository. Format: internal/casebook/FORMAT.md in schuettc/tackle.\nformat_version = %d\n", FormatVersion),
		"policy.toml":   pol,
		"README.md":     []byte(readme),
	} {
		if _, err := r.WriteFile(rel, b); err != nil {
			return nil, err
		}
	}
	if _, err := r.Commit(ctx, "init casebook"); err != nil {
		return nil, err
	}
	if _, err := gitx.Run(ctx, dir, "push", "-q", "-u", "origin", "main"); err != nil {
		return nil, fmt.Errorf("push new casebook: %w", err)
	}
	return r, nil
}

// Open opens an existing casebook clone and checks its format version.
func Open(dir string) (*Repo, error) {
	b, err := os.ReadFile(filepath.Join(dir, "casebook.toml"))
	if err != nil {
		return nil, fmt.Errorf("not a casebook repo (%s): %w", dir, err)
	}
	var m meta
	if _, err := toml.Decode(string(b), &m); err != nil {
		return nil, fmt.Errorf("casebook.toml: %w", err)
	}
	if m.FormatVersion < 1 || m.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("casebook.toml: format_version %d is not supported (max %d); update casebook", m.FormatVersion, FormatVersion)
	}
	return &Repo{Dir: dir, Version: m.FormatVersion}, nil
}

// Upgrade raises an older repo to FormatVersion with one commit ("upgrade
// casebook repo to format N"). Format 2 only adds optional decision fields, so
// no item file changes; older binaries refuse the repo afterwards. It does
// not push. A repo already at FormatVersion is left alone.
func (r *Repo) Upgrade(ctx context.Context) (bool, error) {
	if r.Version >= FormatVersion {
		return false, nil
	}
	b := fmt.Appendf(nil, "# casebook data repository. Format: internal/casebook/FORMAT.md in schuettc/tackle.\nformat_version = %d\n", FormatVersion)
	if _, err := r.WriteFile("casebook.toml", b); err != nil {
		return false, err
	}
	if _, err := r.Commit(ctx, fmt.Sprintf("upgrade casebook repo to format %d", FormatVersion)); err != nil {
		return false, err
	}
	r.Version = FormatVersion
	return true, nil
}

func (r *Repo) abs(rel string) string { return filepath.Join(r.Dir, filepath.FromSlash(rel)) }

// ReadFile reads a repo-relative file.
func (r *Repo) ReadFile(rel string) ([]byte, error) { return os.ReadFile(r.abs(rel)) }

// WriteFile writes a repo-relative file when its content differs.
func (r *Repo) WriteFile(rel string, b []byte) (bool, error) {
	if old, err := os.ReadFile(r.abs(rel)); err == nil && string(old) == string(b) {
		return false, nil
	}
	return true, tools.WriteFileAtomic(r.abs(rel), b, 0o644)
}

// AppendFile appends to a repo-relative file, creating it and its directory.
func (r *Repo) AppendFile(rel string, b []byte) error {
	p := r.abs(rel)
	if err := tools.EnsureDir(filepath.Dir(p)); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Glob matches a slash pattern against the repo and returns sorted relative
// slash paths.
func (r *Repo) Glob(pattern string) ([]string, error) {
	m, err := filepath.Glob(r.abs(pattern))
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(m))
	for _, p := range m {
		rel, _ := filepath.Rel(r.Dir, p)
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}

// Commit stages everything and commits it; false when nothing changed.
func (r *Repo) Commit(ctx context.Context, msg string) (bool, error) {
	if _, err := gitx.Run(ctx, r.Dir, "add", "-A"); err != nil {
		return false, err
	}
	if _, err := gitx.Run(ctx, r.Dir, "diff", "--cached", "--quiet"); err == nil {
		return false, nil
	}
	if _, err := gitx.Run(ctx, r.Dir, "commit", "-q", "--no-verify", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// ReadDecision returns k's decision, or nil when there is none.
func (r *Repo) ReadDecision(k item.Key) (*item.Decision, error) {
	b, err := r.ReadFile(k.File())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d, err := item.DecodeDecision(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", k.File(), err)
	}
	return &d, nil
}

// Decisions reads every decision file, keyed by item key text. Unreadable
// files are returned as errors and skipped.
func (r *Repo) Decisions() (map[string]item.Decision, []error) {
	out := map[string]item.Decision{}
	var errs []error
	root := r.abs("items")
	_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(r.Dir, p)
		rel = filepath.ToSlash(rel)
		k, err := item.KeyFromFile(rel)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		d, err := r.ReadDecision(k)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		out[k.String()] = *d
		return nil
	})
	return out, errs
}

// Policy reads policy.toml.
func (r *Repo) Policy() (item.Policy, error) {
	b, err := r.ReadFile("policy.toml")
	if errors.Is(err, fs.ErrNotExist) {
		return item.DefaultPolicy(), nil
	}
	if err != nil {
		return item.Policy{}, err
	}
	return item.DecodePolicy(b)
}

// Decide validates d, writes k's decision file and commits it. It does not
// push; call Sync.
func (r *Repo) Decide(ctx context.Context, k item.Key, d item.Decision) error {
	if err := d.Validate(k.Kind); err != nil {
		return fmt.Errorf("%s: %w", k, err)
	}
	b, err := item.EncodeDecision(d)
	if err != nil {
		return err
	}
	if _, err := r.WriteFile(k.File(), b); err != nil {
		return err
	}
	msg := fmt.Sprintf("decide %s \u2192 %s", k, d.Disposition)
	if d.Note != "" {
		note := strings.ReplaceAll(d.Note, "\n", " ")
		if rs := []rune(note); len(rs) > 72 {
			note = string(rs[:72]) + "\u2026"
		}
		msg += fmt.Sprintf(" (%q)", note)
	}
	msg += " by " + d.DecidedBy
	_, err = r.Commit(ctx, msg)
	return err
}

// LogEntry is one commit touching a file.
type LogEntry struct {
	Commit  string
	Time    time.Time
	Subject string
}

// Log lists the commits touching rel, newest first.
func (r *Repo) Log(ctx context.Context, rel string) ([]LogEntry, error) {
	out, err := gitx.Run(ctx, r.Dir, "log", "--format=%h%x09%cI%x09%s", "--", rel)
	if err != nil || out == "" {
		return nil, err
	}
	var log []LogEntry
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		at, _ := time.Parse(time.RFC3339, f[1])
		log = append(log, LogEntry{Commit: f[0], Time: at, Subject: f[2]})
	}
	return log, nil
}

// Validate checks every decision file and policy.toml.
func (r *Repo) Validate() []error {
	var errs []error
	root := r.abs("items")
	_ = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(r.Dir, p)
		rel = filepath.ToSlash(rel)
		k, err := item.KeyFromFile(rel)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		d, err := r.ReadDecision(k)
		if err != nil {
			errs = append(errs, err)
			return nil
		}
		if err := d.Validate(k.Kind); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
		}
		return nil
	})
	if _, err := r.Policy(); err != nil {
		errs = append(errs, err)
	}
	return errs
}

// Rules reads every rules/*.toml file. Parse errors are collected in errs and
// those files are skipped.
func (r *Repo) Rules() ([]rules.Rule, []error) {
	paths, err := r.Glob("rules/*.toml")
	if err != nil {
		return nil, []error{err}
	}
	var out []rules.Rule
	var errs []error
	for _, rel := range paths {
		b, err := r.ReadFile(rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		ru, err := rules.Decode(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		out = append(out, ru)
	}
	return out, errs
}

// ReadRule reads a single rule by id. Returns nil, nil when the file is absent.
func (r *Repo) ReadRule(id string) (*rules.Rule, error) {
	if !rules.ValidID(id) {
		return nil, fmt.Errorf("invalid rule id %q", id)
	}
	b, err := r.ReadFile("rules/" + id + ".toml")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ru, err := rules.Decode(b)
	if err != nil {
		return nil, fmt.Errorf("rules/%s.toml: %w", id, err)
	}
	return &ru, nil
}

// WriteRule writes ru as rules/<id>.toml and commits with msg. It does not
// push; call Sync to propagate the change.
func (r *Repo) WriteRule(ctx context.Context, ru rules.Rule, msg string) error {
	if err := ru.Validate(); err != nil {
		return err
	}
	b, err := rules.Encode(ru)
	if err != nil {
		return err
	}
	if _, err := r.WriteFile("rules/"+ru.ID+".toml", b); err != nil {
		return err
	}
	_, err = r.Commit(ctx, msg)
	return err
}

// DeleteRule removes the rules/<id>.toml file and commits with msg.
func (r *Repo) DeleteRule(ctx context.Context, id, msg string) error {
	if !rules.ValidID(id) {
		return fmt.Errorf("invalid rule id %q", id)
	}
	rel := "rules/" + id + ".toml"
	p := filepath.Join(r.Dir, filepath.FromSlash(rel))
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete rule %q: %w", id, err)
	}
	_, err := r.Commit(ctx, msg)
	return err
}
