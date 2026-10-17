package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subaud-tools/tackle/internal/sift/audit"
	"github.com/subaud-tools/tackle/internal/sift/config"
	st "github.com/subaud-tools/tackle/internal/sift/sifttest"
	"github.com/subaud-tools/tackle/skills"
)

// sift check on each skill tackle ships, as a harness installs it, gives
// no findings: the skills hold themselves to what sift asks of every file.
func TestSiftCheckFindsNothingInTheSkills(t *testing.T) {
	for _, name := range []string{"sift", "imprint"} {
		t.Run(name, func(t *testing.T) { checkSkill(t, name) })
	}
}

func checkSkill(t *testing.T, name string) {
	st.Env(t)
	home := st.Home(t)
	t.Setenv("SIFT_HOME", t.TempDir())
	body, err := skills.FS.ReadFile(name + "/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\ndescription: ") {
		t.Fatalf("%s has no description in its front matter", name)
	}
	if len(body) > config.Default().Budgets.Skill {
		t.Fatalf("the skill is %d bytes, over its budget", len(body))
	}
	st.Write(t, home, ".claude/skills/"+name+"/SKILL.md", string(body))
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
