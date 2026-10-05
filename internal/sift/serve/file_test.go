package serve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/row"
	"github.com/schuettc/tackle/internal/sift/store"
)

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func retarget(t *testing.T, link, to string) {
	t.Helper()
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(to, link); err != nil {
		t.Fatal(err)
	}
}

// A row read from disk is read where it resolved when it was audited: a
// symlink (leaf or parent) retargeted since is refused, as is a stored path
// with "..", and a row id from another round.
func TestFileFromDiskIsTheAuditedFile(t *testing.T) {
	f := newFixture(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	mustWrite(t, filepath.Join(dir, "a.md"), "audited\n")
	mustWrite(t, filepath.Join(dir, "b.md"), "secret elsewhere\n")
	mustWrite(t, filepath.Join(dir, "one", "f.md"), "audited parent\n")
	mustWrite(t, filepath.Join(dir, "two", "f.md"), "secret parent\n")
	mustWrite(t, filepath.Join(dir, "sub", "x.md"), "dotdot\n")
	leaf := filepath.Join(dir, "leaf.md")
	retarget(t, leaf, filepath.Join(dir, "a.md"))
	par := filepath.Join(dir, "par")
	retarget(t, par, filepath.Join(dir, "one"))
	rows := []row.Row{
		{ID: "leaf", Check: "size", Source: row.Source{File: leaf}},
		{ID: "parent", Check: "size", Source: row.Source{File: filepath.Join(par, "f.md")}},
		{ID: "dotdot", Check: "size", Source: row.Source{File: filepath.Join(dir, "sub") + "/../sub/x.md"}},
	}
	id, err := f.st.RecordRound(context.Background(), store.Round{Kind: "on-demand"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	get := func(round int64, rowID string) (int, string) {
		w := f.do("GET", fmt.Sprintf("/api/file?round=%d&id=%s", round, rowID), "")
		return w.Code, w.Body.String()
	}
	if c, b := get(id, "leaf"); c != 200 || !strings.Contains(b, "audited") {
		t.Fatalf("leaf before: %d %s", c, b)
	}
	if c, b := get(id, "parent"); c != 200 || !strings.Contains(b, "audited parent") {
		t.Fatalf("parent before: %d %s", c, b)
	}
	retarget(t, leaf, filepath.Join(dir, "b.md"))
	retarget(t, par, filepath.Join(dir, "two"))
	for _, rowID := range []string{"leaf", "parent", "dotdot"} {
		if c, b := get(id, rowID); c != 403 || strings.Contains(b, "secret") || strings.Contains(b, "dotdot\\n") {
			t.Errorf("%s: %d %s", rowID, c, b)
		}
	}
	// A row id from another round: the old round is stale, and the latest
	// round has no such row.
	if c, _ := get(f.round, "r-neg"); c != 409 {
		t.Errorf("old round: %d", c)
	}
	if c, _ := get(id, "r-neg"); c != 404 {
		t.Errorf("another round's row in the latest round: %d", c)
	}
}
