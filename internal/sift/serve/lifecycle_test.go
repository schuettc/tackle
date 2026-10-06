package serve

import (
	"os"
	"os/exec"
	"reflect"
	"sync"
	"testing"
)

func TestStartPassesDetachedFlags(t *testing.T) {
	t.Setenv("SIFT_HOME", t.TempDir())
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
		adv, err := Start("/bin/sift", tc.port)
		newCommand = orig
		if err != nil {
			t.Fatal(err)
		}
		if gotExe != "/bin/sift" || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("port %d: exe %q args %v, want %v", tc.port, gotExe, got, tc.want)
		}
		if p, _ := os.FindProcess(adv.PID); p != nil {
			_ = p.Kill()
		}
		_ = os.Remove(AdvertPath())
	}
}

func TestConcurrentStartsShareRunningServe(t *testing.T) {
	t.Setenv("SIFT_HOME", t.TempDir())
	if err := os.MkdirAll(LiveDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	orig := newCommand
	defer func() { newCommand = orig }()
	// Like serve: the first to claim the advert wins; later ones exit.
	script := `if mkdir "$1.lock" 2>/dev/null; then printf '{"url":"u","base":"b","pid":%d}' $$ > "$1"; sleep 5; else sleep 0.3; exit 1; fi`
	newCommand = func(string, ...string) *exec.Cmd {
		return exec.Command("sh", "-c", script, "sh", AdvertPath())
	}
	var wg sync.WaitGroup
	advs := make([]Advert, 2)
	errs := make([]error, 2)
	for i := range advs {
		wg.Add(1)
		go func() { defer wg.Done(); advs[i], errs[i] = Start("/bin/sift", 0) }()
	}
	wg.Wait()
	defer func() {
		if p, _ := os.FindProcess(advs[0].PID); p != nil {
			_ = p.Kill()
		}
	}()
	for i := range errs {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
	}
	if advs[0].PID != advs[1].PID || advs[0].PID == 0 {
		t.Errorf("pids differ: %d vs %d", advs[0].PID, advs[1].PID)
	}
}
