package proj

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	tools "github.com/schuettc/tools-common"
)

// hookEvents are the tmux events after which a server's sessions are
// snapshotted into the record. after-set-option is the one that matters most:
// it fires when an agent's announcer writes @harness_session, which is how a
// /resume inside pi reaches the record. session-closed is deliberately absent;
// the record never shrinks on tmux events (see record.go).
var hookEvents = []string{"session-created", "after-set-option", "session-renamed"}

// layoutEvents are the tmux events after which the Ghostty layout is saved: a
// client attaching (a Ghostty tab opening on a session) or a client switching
// session (the tab now shows another one). Ghostty itself reports nothing when
// tabs move, so these are the moments the layout is known to change.
var layoutEvents = []string{"client-attached", "client-session-changed"}

// hookIndex is proj's slot in each hook array. A fixed index makes installing
// idempotent (re-setting [80] replaces it) and leaves the operator's own hooks,
// which default to [0], alone.
const hookIndex = "80"

// HookCommand is the tmux command proj installs on each hook event. -b keeps
// the snapshot off tmux's command queue so a slow disk never stalls the server.
func HookCommand(exe, socket string) string {
	return `run-shell -b "` + shellQuote(exe) + ` __snapshot --socket ` + shellQuote(socket) + `"`
}

// LayoutHookCommand is the tmux command proj installs on each layout event.
func LayoutHookCommand(exe string) string {
	return `run-shell -b "` + shellQuote(exe) + ` __save-layout"`
}

// InstallHooks points socket's snapshot and layout hooks at the running proj binary.
// Best-effort, like every other tmux option write proj makes. It installs
// nothing unless the running binary is named proj: under `go test` it is the
// test binary, and a hook pointing there would re-run the suite on every
// option change.
func InstallHooks(socket string) {
	exe, err := executable()
	if err != nil || filepath.Base(exe) != "proj" {
		return
	}
	installHooksExe(socket, exe)
}

// executable resolves the running binary; a seam so tests can present one
// named proj.
var executable = os.Executable

func installHooksExe(socket, exe string) {
	for _, ev := range hookEvents {
		_, _ = runner(socket, "set-hook", "-g", ev+"["+hookIndex+"]", HookCommand(exe, socket))
	}
	for _, ev := range layoutEvents {
		_, _ = runner(socket, "set-hook", "-g", ev+"["+hookIndex+"]", LayoutHookCommand(exe))
	}
}

// layoutSettle is how long a layout save waits for later attaches. A restore
// attaches a tab every second or so; each attach restarts the wait, so the
// layout is read once, after the last tab, rather than once per tab.
var layoutSettle = 2 * time.Second

// SaveLayoutSettled is the attach hook's save: it marks itself the latest
// request, waits layoutSettle, and saves only if no later request replaced
// the mark. The title read also needs the wait: tmux sets the tab title just
// after the attach that fired the hook.
func SaveLayoutSettled() error {
	p := filepath.Join(tools.StateDir("proj"), "layout.pending")
	if err := tools.EnsureDir(filepath.Dir(p)); err != nil {
		return err
	}
	tok := fmt.Sprintf("%d.%d", os.Getpid(), time.Now().UnixNano())
	if err := tools.WriteFileAtomic(p, []byte(tok), 0o600); err != nil {
		return err
	}
	time.Sleep(layoutSettle)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if string(b) != tok {
		return nil // a later attach owns the save
	}
	_, _, err = SaveLayout(CurrentLiveState().Running)
	return err
}

// PrimeAll installs hooks on, and snapshots, every live proj server. The
// picker runs it once at launch so servers created before this feature (or by
// other tools) are recorded without waiting for one of their hooks to fire.
func PrimeAll() {
	for _, sock := range Servers() {
		InstallHooks(sock)
		_ = Snapshot(sock)
	}
}

// Snapshot upserts socket's live sessions into the record. A server that is
// gone or empty contributes nothing and removes nothing.
func Snapshot(socket string) error {
	live := LiveEntries(socket, DefaultTranscripts())
	if len(live) == 0 {
		return nil
	}
	return UpdateRecord(func(r *Record) bool { return mergeLive(r, live, time.Now()) })
}

// LiveEntries observes socket's sessions as record entries. The agent comes
// from where the conversation's transcript lives, else from what the main
// pane is running, else from what proj recorded launching.
func LiveEntries(socket string, t Transcripts) []LiveEntry {
	names, err := Run(socket, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil
	}
	project := ProjectFromSocket(socket)
	var out []LiveEntry
	for _, name := range splitLines(names) {
		if name == "" {
			continue
		}
		pane, cmd := mainPaneCmd(socket, name)
		conv := Query(socket, name, "#{@harness_session}")
		agent, ok := t.Locate(conv)
		if !ok {
			switch k := classifyAgent(cmd); k {
			case "pi", "claude", "cursor":
				agent = k
			default:
				agent = Query(socket, name, "#{"+AgentOption()+"}")
			}
		}
		out = append(out, LiveEntry{Name: name, Entry: Entry{
			Socket:       socket,
			Project:      project,
			Dir:          Query(socket, pane, "#{pane_current_path}"),
			Agent:        agent,
			Conversation: conv,
		}})
	}
	return out
}
