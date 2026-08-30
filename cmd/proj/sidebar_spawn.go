package main

import (
	"os"
	"os/exec"
	"syscall"
)

// sidebarArgv returns the argv for `proj sidebar <session> --socket <socket> --dir <dir>`.
// It is a pure helper (no side-effects) that can be tested without spawning.
func sidebarArgv(exe, socket, session, dir string) []string {
	return []string{exe, "sidebar", session, "--socket", socket, "--dir", dir}
}

// SpawnSidebarDetached fires `proj sidebar <session> --socket <socket> --dir <dir>`
// as a fully detached child process (new process group, no Wait). The sidebar
// build therefore outlives the picker exit or CLI invocation.
//
// Guard: if os.Executable() fails we silently no-op — never block the session.
func SpawnSidebarDetached(socket, session, dir string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	argv := sidebarArgv(exe, socket, session, dir)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Ignore Start errors: sidebar is best-effort.
	_ = cmd.Start()
}
