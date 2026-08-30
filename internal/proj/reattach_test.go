package proj

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGotoEmitFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "exec")
	t.Setenv("PROJ_EXEC", f)
	t.Setenv("TMUX", "") // force the outside-tmux "print" path
	if err := Goto("proj-x", "proj-x/w"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(b))
	if got != "exec tmux -L proj-x attach -t =proj-x/w" {
		t.Fatalf("emitted %q", got)
	}
}

func TestPlanGoto(t *testing.T) {
	if a := PlanGoto("", "proj-x", "proj-x/w"); a.Kind != "print" ||
		a.Print != "exec tmux -L proj-x attach -t =proj-x/w" {
		t.Fatalf("outside-tmux: %+v", a)
	}
	if a := PlanGoto("proj-x", "proj-x", "proj-x/w"); a.Kind != "switch" ||
		!reflect.DeepEqual(a.Cmd, []string{"switch-client", "-t", "=proj-x/w"}) {
		t.Fatalf("same-server: %+v", a)
	}
	if a := PlanGoto("proj-a", "proj-x", "proj-x/w"); a.Kind != "detach" ||
		!reflect.DeepEqual(a.Cmd, []string{"detach", "-E", "tmux -L proj-x attach -t =proj-x/w"}) {
		t.Fatalf("cross-server: %+v", a)
	}
}
