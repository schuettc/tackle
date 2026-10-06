package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/schuettc/tackle/internal/sift/audit"
	"github.com/schuettc/tackle/internal/sift/config"
	st "github.com/schuettc/tackle/internal/sift/sifttest"
	"github.com/schuettc/tackle/skills"
)

// sift check on the sift skill, as a harness installs it, gives no
// findings: the skill holds itself to what sift asks of every file.
func TestSiftCheckFindsNothingInTheSiftSkill(t *testing.T) {
	st.Env(t)
	home := st.Home(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	body, err := skills.FS.ReadFile("sift/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > config.Default().Budgets.Skill {
		t.Fatalf("the skill is %d bytes, over its budget", len(body))
	}
	st.Write(t, home, ".claude/skills/sift/SKILL.md", string(body))
	cfg := config.Default()
	cfg.Profiles = []string{"claude-code"}
	rep, err := audit.Run(context.Background(), audit.Options{Config: cfg, LookPath: func(string) (string, error) { return "", os.ErrNotExist }})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 1 {
		t.Fatalf("audited %d files, want the installed skill", rep.Files)
	}
	for _, r := range rep.Rows {
		t.Errorf("%s %s:%d: %s %q", r.Check, filepath.Base(filepath.Dir(r.Source.File)), r.Source.Start, r.Summary, r.Passage)
	}
}
