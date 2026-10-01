package serve

import (
	"os"
	"os/exec"
	"reflect"
	"testing"
)

func TestStartPassesDetachedFlags(t *testing.T) {
	t.Setenv("CULL_HOME", t.TempDir())
	for _, tc := range []struct {
		port int
		want []string
	}{
		{0, []string{"serve", "--foreground", "--no-open"}},
		{4321, []string{"serve", "--foreground", "--no-open", "--port", "4321"}},
	} {
		var gotExe string
		var got []string
		orig := newCommand
		newCommand = func(exe string, args ...string) *exec.Cmd {
			gotExe, got = exe, args
			// A stand-in server: advertises its own pid, then lingers.
			script := `printf '{"url":"u","base":"b","pid":%d}' $$ > "$1"; sleep 3`
			return exec.Command("sh", "-c", script, "sh", AdvertPath())
		}
		if err := os.MkdirAll(LiveDir(), 0o700); err != nil {
			t.Fatal(err)
		}
		adv, err := Start("/bin/cull", tc.port)
		newCommand = orig
		if err != nil {
			t.Fatal(err)
		}
		if gotExe != "/bin/cull" || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("port %d: exe %q args %v, want %v", tc.port, gotExe, got, tc.want)
		}
		if p, _ := os.FindProcess(adv.PID); p != nil {
			_ = p.Kill()
		}
		_ = os.Remove(AdvertPath())
	}
}
