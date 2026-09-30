package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/casebook/gitx"
	"github.com/schuettc/tackle/internal/casebook/testgit"
)

func TestAppendRestoreWritesHeaderAndCommits(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	r := &Repo{Dir: testgit.NewRepo(t)}

	rec1 := RestoreRecord{
		Key:            "branch:schuettc/hail@feat/x",
		Action:         "branch-delete-local",
		Before:         "deadbeef",
		RestoreCommand: "git -C '/x' branch 'feat/x' deadbeef",
	}
	committed, err := r.AppendRestore(ctx, "2026-09-27", rec1)
	if err != nil {
		t.Fatalf("AppendRestore: %v", err)
	}
	if !committed {
		t.Fatal("expected a commit")
	}

	rel := filepath.Join(r.Dir, "restores", "2026-09-27.tsv")
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d; want header + 1 record", len(lines))
	}
	if lines[0] != "key\taction\tbefore\trestore-command" {
		t.Errorf("header = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "branch:schuettc/hail@feat/x\tbranch-delete-local\tdeadbeef\t") {
		t.Errorf("record = %q", lines[1])
	}

	// The commit message names the key and action.
	msg, err := gitx.Run(ctx, r.Dir, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "restore record for branch:schuettc/hail@feat/x (branch-delete-local)" {
		t.Errorf("commit message = %q", msg)
	}

	// A second append does not rewrite the header and commits again.
	rec2 := RestoreRecord{Key: "branch:schuettc/hail@feat/y", Action: "branch-delete-local", Before: "cafe", RestoreCommand: "git -C '/x' branch 'feat/y' cafe"}
	if _, err := r.AppendRestore(ctx, "2026-09-27", rec2); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(rel)
	lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %d; want header + 2 records", len(lines))
	}
	if strings.Count(string(b), "key\taction") != 1 {
		t.Error("header written more than once")
	}
}

func TestAppendRestoreFailsOnNonRepo(t *testing.T) {
	testgit.Env(t)
	ctx := context.Background()
	r := &Repo{Dir: t.TempDir()} // not a git repo
	_, err := r.AppendRestore(ctx, "2026-09-27", RestoreRecord{Key: "k", Action: "a", Before: "b", RestoreCommand: "c"})
	if err == nil {
		t.Fatal("expected an error committing to a non-repo")
	}
}
