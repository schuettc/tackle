package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/rec"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/internal/sift/store"
)

// A skill installed as a symlink into a repo outside every root is read at
// that repo's base, as the audit records it (its [[repo]] base here), and
// apply cuts the branch from that recorded base: changing the base in the
// config after approval does not move it.
func TestASymlinkedSkillIsAppliedAtTheBaseItsAuditRead(t *testing.T) {
	home := siftEnv(t)
	ctx := context.Background()
	const onMain = "---\nname: dispatch\n---\n\n# Dispatch\n\n- main's rule\n"
	const onDev = "---\nname: dispatch\n---\n\n# Dispatch\n\n- waiting on the new runner\n"
	dots := st.Repo(t, filepath.Join(t.TempDir(), "dotfiles"), map[string]string{"skills/dispatch/SKILL.md": onMain})
	st.Publish(t, dots)
	st.Git(t, dots, "checkout", "-q", "-b", "dev")
	st.Commit(t, dots, map[string]string{"skills/dispatch/SKILL.md": onDev})
	st.Git(t, dots, "push", "-q", "-u", "origin", "dev")
	// The checkout differs from both: the audit reads the base, not disk.
	st.Write(t, dots, "skills/dispatch/SKILL.md", onDev+"- an edit not pushed\n")
	link := filepath.Join(home, ".claude", "skills", "dispatch", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dots, "skills/dispatch/SKILL.md"), link); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Profiles = []string{"claude-code"}
	cfg.Roots = []config.Root{{Path: t.TempDir()}}
	cfg.Repos = []config.Repo{{Path: dots, Base: "dev"}}
	if err := config.Save(config.Path(), cfg); err != nil {
		t.Fatal(err)
	}
	if code, out, errw := run(t, "", "check"); code > 1 {
		t.Fatalf("check %d: %s %s", code, out, errw)
	}

	s, err := store.Open(ctx, store.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	items, err := s.Files(ctx, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("files %+v %v", items, err)
	}
	f := items[0]
	real, _ := filepath.EvalSymlinks(dots)
	if f.Source.File != link || f.Source.Repo != real || f.Source.Ref != "origin/dev" || f.Source.Path != "skills/dispatch/SKILL.md" || f.Base != rec.Hash(onDev) {
		t.Fatalf("the audit read %+v (base %s, want dev's %s)", f.Source, f.Base, rec.Hash(onDev))
	}
	var accounts []rec.Account
	for _, r := range f.Rows {
		accounts = append(accounts, rec.Account{Row: r, Did: "fixed", How: "removed"})
	}
	want := "---\nname: dispatch\n---\n\n# Dispatch\n"
	if _, err := s.Propose(ctx, 1, []rec.Rec{{File: f.Key, Base: f.Base, Content: want, Summary: "s", Findings: accounts}}); err != nil {
		t.Fatal(err)
	}
	if items, err = s.Files(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideFile(ctx, 1, f.Key, rec.Decision{Action: "accept"}, map[string]store.Seen{f.Key: {Fingerprint: items[0].Fingerprint}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, 1, "", nil); err != nil {
		t.Fatal(err)
	}

	// Apply holds a dirty primary clone: the edit goes before it runs.
	st.Git(t, dots, "checkout", "-q", "--", ".")
	cfg.Repos = []config.Repo{{Path: dots, Base: "main"}}
	if err := config.Save(config.Path(), cfg); err != nil {
		t.Fatal(err)
	}
	code, out, errw := run(t, "", "apply")
	if code != 0 || !strings.Contains(out, "branch sift/round-1 (1 applied, 0 skipped)") {
		t.Fatalf("apply %d: %s %s", code, out, errw)
	}
	if got, dev := st.Git(t, dots, "rev-parse", "sift/round-1^"), st.Git(t, dots, "rev-parse", "origin/dev"); got != dev {
		t.Errorf("the branch's parent is %s, not dev's %s", got, dev)
	}
	if got := st.Git(t, dots, "show", "sift/round-1:skills/dispatch/SKILL.md"); got+"\n" != want {
		t.Errorf("on the branch:\n%s", got)
	}
}
