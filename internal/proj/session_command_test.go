package proj

import (
	"errors"
	"slices"
	"testing"
)

type recordedCall struct {
	socket string
	args   []string
}

// withFakeRun swaps the runner + newSessionHome seams so EnsureSession's tmux
// calls are captured instead of executed. has-session returns an error, forcing
// the create path. Returns a pointer to the recorded call slice.
func withFakeRun(t *testing.T) *[]recordedCall {
	t.Helper()
	var calls []recordedCall
	origRunner, origNew := runner, newSessionHome
	t.Cleanup(func() { runner, newSessionHome = origRunner, origNew })
	newSessionHome = func(socket, name string) error { return nil }
	runner = func(socket string, args ...string) (string, error) {
		calls = append(calls, recordedCall{socket, append([]string(nil), args...)})
		if len(args) > 0 && args[0] == "has-session" {
			return "", errors.New("no such session")
		}
		return "", nil
	}
	return &calls
}

func find(calls []recordedCall, verb string) *recordedCall {
	for i := range calls {
		if len(calls[i].args) > 0 && calls[i].args[0] == verb {
			return &calls[i]
		}
	}
	return nil
}

func TestEnsureSessionCommandRunsInPaneAndSkipsSendKeys(t *testing.T) {
	calls := withFakeRun(t)
	cmd := []string{"hail", "shim", "--id", "arg with spaces", "-x"}
	created, err := EnsureSession("proj-x", "x/w", "/tmp/dir", "pi", "", cmd)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v; want true,nil", created, err)
	}

	respawn := find(*calls, "respawn-pane")
	if respawn == nil {
		t.Fatal("no respawn-pane call")
	}
	// respawn-pane -k -t =x/w: -c /tmp/dir -- hail shim --id "arg with spaces" -x
	want := []string{"respawn-pane", "-k", "-t", "=x/w:", "-c", "/tmp/dir", "--", "hail", "shim", "--id", "arg with spaces", "-x"}
	if !slices.Equal(respawn.args, want) {
		t.Fatalf("respawn-pane args =\n  %q\nwant\n  %q", respawn.args, want)
	}
	// A command replaces the shell launch: send-keys must NOT be issued.
	if find(*calls, "send-keys") != nil {
		t.Fatal("send-keys issued when --command was given")
	}
	// The agent is still recorded for proj list.
	setOpt := findAgentOption(*calls)
	if setOpt == nil {
		t.Fatalf("@proj_agent not set; calls=%v", *calls)
	}
	if got := setOpt.args[len(setOpt.args)-1]; got != "pi" {
		t.Fatalf("@proj_agent = %q, want pi", got)
	}
}

func TestEnsureSessionNoCommandDoesNotAppendArgv(t *testing.T) {
	calls := withFakeRun(t)
	// agent "none" keeps send-keys deterministic (agentLaunchCmd returns "" and
	// the send-keys branch is gated on agent != none anyway).
	created, err := EnsureSession("proj-x", "x/w", "/tmp/dir", "none", "", nil)
	if err != nil || !created {
		t.Fatalf("created=%v err=%v; want true,nil", created, err)
	}
	respawn := find(*calls, "respawn-pane")
	if respawn == nil {
		t.Fatal("no respawn-pane call")
	}
	if slices.Contains(respawn.args, "--") {
		t.Fatalf("respawn-pane carried a command with no --command given: %q", respawn.args)
	}
}

func TestEnsureSessionReuseIsUntouched(t *testing.T) {
	var calls []recordedCall
	origRunner, origNew := runner, newSessionHome
	t.Cleanup(func() { runner, newSessionHome = origRunner, origNew })
	newSessionHome = func(socket, name string) error {
		t.Fatal("newSessionHome called on reuse")
		return nil
	}
	runner = func(socket string, args ...string) (string, error) {
		calls = append(calls, recordedCall{socket, append([]string(nil), args...)})
		return "", nil // has-session succeeds -> reuse
	}
	created, err := EnsureSession("proj-x", "x/w", "/tmp/dir", "pi", "", []string{"hail", "shim"})
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if created {
		t.Fatal("created=true on reuse; want false")
	}
	// Only the has-session probe should have run; no respawn/set-option/send-keys.
	if len(calls) != 1 || calls[0].args[0] != "has-session" {
		t.Fatalf("reuse issued extra calls: %v", calls)
	}
}

// findAgentOption returns the set-option call that writes AgentOption(), if any.
func findAgentOption(calls []recordedCall) *recordedCall {
	for i := range calls {
		a := calls[i].args
		if len(a) >= 2 && a[0] == "set-option" && slices.Contains(a, AgentOption()) {
			return &calls[i]
		}
	}
	return nil
}

func sendKeysText(calls []recordedCall) string {
	if sk := find(calls, "send-keys"); sk != nil && len(sk.args) >= 4 {
		return sk.args[3]
	}
	return ""
}

func TestRestoreSessionTypesResume(t *testing.T) {
	stubBins(t, "pi", "claude")
	calls := withFakeRun(t)
	created, err := RestoreSession("proj-x", "x/w", "/tmp/dir", "pi", "id1")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if got := sendKeysText(*calls); got != "pi --session 'id1'" {
		t.Fatalf("typed %q want pi --session 'id1'", got)
	}
	if opt := findAgentOption(*calls); opt == nil || opt.args[len(opt.args)-1] != "pi" {
		t.Fatalf("@proj_agent not set to pi; calls=%v", *calls)
	}

	calls = withFakeRun(t)
	if _, err := RestoreSession("proj-x", "x/w", "/tmp/dir", "pi", ""); err != nil {
		t.Fatal(err)
	}
	if got := sendKeysText(*calls); got != "pi --name 'x/w'" {
		t.Fatalf("no conversation: typed %q want a fresh pi --name 'x/w'", got)
	}
}

func TestRestoreSessionReuseLeavesPane(t *testing.T) {
	var calls []recordedCall
	orig := runner
	t.Cleanup(func() { runner = orig })
	runner = func(socket string, args ...string) (string, error) {
		calls = append(calls, recordedCall{socket, args})
		return "", nil // has-session succeeds: the session exists
	}
	created, err := RestoreSession("proj-x", "x/w", "/tmp/dir", "pi", "id1")
	if err != nil || created {
		t.Fatalf("created=%v err=%v; want false,nil", created, err)
	}
	if find(calls, "send-keys") != nil || find(calls, "respawn-pane") != nil {
		t.Fatalf("existing session was touched: %v", calls)
	}
}

func TestEnsureSessionInstallsHooks(t *testing.T) {
	calls := withFakeRun(t)
	orig := executable
	t.Cleanup(func() { executable = orig })
	executable = func() (string, error) { return "/x/proj", nil }
	if _, err := EnsureSession("proj-x", "x/w", "/tmp/dir", "none", "", nil); err != nil {
		t.Fatal(err)
	}
	var hooks []string
	for _, c := range *calls {
		if len(c.args) >= 3 && c.args[0] == "set-hook" {
			hooks = append(hooks, c.args[2])
		}
	}
	if !slices.Contains(hooks, "session-created[80]") || !slices.Contains(hooks, "after-set-option[80]") {
		t.Fatalf("hooks installed = %v", hooks)
	}
}
