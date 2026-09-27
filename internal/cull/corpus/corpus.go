// Package corpus is cull's labeled example set: Jev states snapshotted with a
// person's keep/cut/review label, split into dev (tuning) and holdout (final
// check). It lives outside any repo because it holds private source.
package corpus

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/policy"
	tools "github.com/schuettc/tools-common"
)

// Entry is one example: the exact state Jev sees, plus its label if any.
type Entry struct {
	ID        string         `json:"id"`
	Repo      string         `json:"repo,omitempty"`
	State     judge.State    `json:"state"`
	StateHash string         `json:"state_hash"`
	Split     string         `json:"split"` // "dev" | "holdout"
	Label     policy.Verdict `json:"label,omitempty"`
	LabeledBy string         `json:"labeled_by,omitempty"`
	Note      string         `json:"note,omitempty"`
	LabeledAt time.Time      `json:"labeled_at,omitzero"`
}

// Store is the corpus file loaded in memory; Save writes it back.
type Store struct {
	Path    string
	entries map[string]Entry
}

// DefaultPath is the corpus under the cull data directory.
func DefaultPath() string {
	return filepath.Join(tools.DataDir("cull"), "corpus", "corpus.jsonl")
}

// Open loads the corpus at path; a missing file is an empty corpus.
func Open(path string) (*Store, error) {
	s := &Store{Path: path, entries: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 64<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		s.entries[e.ID] = e
	}
	return s, sc.Err()
}

// Entries returns every entry sorted by ID.
func (s *Store) Entries() []Entry {
	out := make([]Entry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ImportResult counts what an import did.
type ImportResult struct{ Added, Updated, Unchanged int }

// Import adds new tests and refreshes changed ones. A changed test loses its
// label: a judgment of old code must not carry onto new code.
func (s *Store) Import(tcs []cases.TestCase) ImportResult {
	var r ImportResult
	for _, tc := range tcs {
		st := judge.StateFor(tc)
		h := judge.StateHash(st)
		old, ok := s.entries[tc.ID]
		switch {
		case !ok:
			s.entries[tc.ID] = Entry{ID: tc.ID, Repo: tc.Repo, State: st, StateHash: h, Split: SplitFor(tc.ID)}
			r.Added++
		case old.StateHash == h:
			r.Unchanged++
		default:
			s.entries[tc.ID] = Entry{ID: tc.ID, Repo: tc.Repo, State: st, StateHash: h, Split: old.Split}
			r.Updated++
		}
	}
	return r
}

// Label records a person's judgment of one entry.
func (s *Store) Label(id string, v policy.Verdict, by, note string, now time.Time) error {
	e, ok := s.entries[id]
	if !ok {
		return fmt.Errorf("no corpus entry %q", id)
	}
	e.Label, e.LabeledBy, e.Note, e.LabeledAt = v, by, note, now.UTC()
	s.entries[id] = e
	return nil
}

// Save writes the corpus atomically, 0600, creating its directory 0700.
func (s *Store) Save() error {
	if err := tools.EnsureDir(filepath.Dir(s.Path)); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, e := range s.Entries() {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return tools.WriteFileAtomic(s.Path, buf.Bytes(), 0o600)
}

// ReadCases parses TestCase JSONL.
func ReadCases(r io.Reader) ([]cases.TestCase, error) {
	var out []cases.TestCase
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, 64<<20)
	for n := 1; sc.Scan(); n++ {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var tc cases.TestCase
		if err := json.Unmarshal(sc.Bytes(), &tc); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if tc.ID == "" {
			return nil, fmt.Errorf("line %d: missing id", n)
		}
		out = append(out, tc)
	}
	return out, sc.Err()
}

// SplitFor assigns about 30% of IDs to holdout, stably.
func SplitFor(id string) string {
	sum := sha256.Sum256([]byte(id))
	if sum[0]%10 < 3 {
		return "holdout"
	}
	return "dev"
}
