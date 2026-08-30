package proj

import "reflect"
import "testing"

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
