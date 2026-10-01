package serve

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/schuettc/tools-common/localweb"
)

// Stop asks the live serve to exit and waits for its advert to go.
func Stop(ctx context.Context) error {
	adv, err := Running()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adv.Base+"/api/stop", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set(localweb.TokenHeader, adv.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	for i := 0; i < 50; i++ {
		if _, err := Running(); errors.Is(err, ErrNotRunning) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("cull serve (pid %d) did not stop", adv.PID)
}

// Start runs exe as `cull serve --foreground` in its own process group,
// logging to StateDir/serve.log, and waits for its advert.
func Start(exe string, port int) (Advert, error) {
	if exe == "" {
		return Advert{}, errors.New("can't find the cull binary to start the server")
	}
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return Advert{}, err
	}
	logPath := filepath.Join(StateDir(), "serve.log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Advert{}, err
	}
	defer func() { _ = logf.Close() }()
	args := []string{"serve", "--foreground"}
	if port > 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return Advert{}, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	for i := 0; i < 100; i++ {
		if adv, err := Running(); err == nil && adv.PID == pid {
			return adv, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return Advert{}, fmt.Errorf("cull serve (pid %d) didn't start; see %s", pid, logPath)
}
