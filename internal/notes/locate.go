package notes

import (
	"fmt"
	"hash/fnv"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Pad files live in a per-user store outside any repository. Keying them by
// the working directory — and writing them into it — meant two coding-agent
// sessions open in one checkout shared a single pad and overwrote each
// other's notes, and that every repo grew a file it had to ignore.
//
// The key is the agent session when there is one, so a pad follows the
// conversation: a resumed session keeps its UUID and so reopens its own pad,
// even from a different pane, tab, or worktree. With no agent but inside
// tmux, the key is the tmux session name — two shell-only sessions open in
// one checkout are still two workspaces and get two pads. Only outside tmux
// does the key come from the directory. Either way the file lands in the
// store, never in the tree.
const (
	// EnvFile pins the exact pad file and skips all derivation. It is what
	// keeps a human TUI and an agent shelling out to `scratch append`
	// pointed at the same file when something upstream has already decided
	// which pad is in play.
	EnvFile = "SCRATCH_FILE"
	// EnvDir overrides the store root.
	EnvDir = "SCRATCH_DIR"
	// EnvClaudeSession is Claude Code's session UUID, present in the
	// environment of the session process and anything it spawns.
	EnvClaudeSession = "CLAUDE_CODE_SESSION_ID"
	// TmuxOption is the tmux session option a harness SessionStart hook
	// stamps with the agent's session id. It is how a scratch pane — a
	// SIBLING of the agent's pane, which therefore does not inherit the
	// agent's environment — learns which session it belongs to.
	TmuxOption = "@harness_session"
	// maxKey bounds the filename so a deeply nested directory key cannot
	// exceed the filesystem's per-component limit.
	maxKey = 180
)

// ambient is the set of environment lookups path resolution depends on,
// injected so the derivation can be tested without a real tmux, a real
// HOME, or a real agent session.
type ambient struct {
	getenv      func(string) string
	tmuxOpt     func(string) string
	tmuxSession func() string
	configDir   func() (string, error)
}

func realAmbient() ambient {
	return ambient{
		getenv:      os.Getenv,
		tmuxOpt:     tmuxOption,
		tmuxSession: tmuxSessionName,
		configDir:   os.UserConfigDir,
	}
}

// Path returns the pad file for this invocation, given the working
// directory. It returns an error only when the store location cannot be
// determined at all; callers must not silently fall back to a path inside
// cwd, which is the outcome the store exists to prevent.
func Path(cwd string) (string, error) {
	return resolve(cwd, realAmbient())
}

func resolve(cwd string, a ambient) (string, error) {
	if f := strings.TrimSpace(a.getenv(EnvFile)); f != "" {
		return f, nil
	}
	root, err := storeRoot(a)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "pads", padKey(cwd, a)+".md"), nil
}

// storeRoot is the directory holding every pad. os.UserConfigDir is the
// only stdlib location that is durable on all three platforms — macOS
// ~/Library/Application Support, Linux $XDG_CONFIG_HOME or ~/.config,
// Windows %AppData%. The cache directory would be wrong: pads are notes a
// person wrote, and caches are expected to be purgeable.
func storeRoot(a ambient) (string, error) {
	if d := strings.TrimSpace(a.getenv(EnvDir)); d != "" {
		return d, nil
	}
	cfg, err := a.configDir()
	if err != nil {
		return "", fmt.Errorf("locate scratch store (set %s to choose one): %w", EnvDir, err)
	}
	return filepath.Join(cfg, "scratch"), nil
}

// padKey identifies the pad: the agent session if this process can see one,
// else the tmux session it runs in, else the working directory.
func padKey(cwd string, a ambient) string {
	if id := sessionID(a); id != "" {
		return safeKey(id)
	}
	if a.tmuxSession != nil {
		if name := strings.TrimSpace(a.tmuxSession()); name != "" {
			return safeKey(name)
		}
	}
	return safeKey(dirKey(cwd))
}

// sessionID finds the agent session two ways, in order of directness: the
// environment (this process is the agent or a child of it), then the tmux
// session option (this process is a sibling pane and must be told).
func sessionID(a ambient) string {
	if id := strings.TrimSpace(a.getenv(EnvClaudeSession)); id != "" {
		return id
	}
	return strings.TrimSpace(a.tmuxOpt(TmuxOption))
}

// dirKey renders a directory as a readable single token: /Users/c/dotfiles
// becomes -Users-c-dotfiles.
func dirKey(cwd string) string {
	if cwd == "" {
		return "no-cwd"
	}
	return strings.NewReplacer(string(filepath.Separator), "-", "/", "-", `\`, "-", ":", "-").Replace(cwd)
}

// safeKey reduces s to a filename-safe token. Beyond tidiness this is a
// containment boundary: the session id arrives from the environment or from
// a tmux option, and an unsanitised "../.." in either would place the pad
// outside the store.
func safeKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	key := b.String()
	// "." and ".." survive the loop intact and would still traverse.
	if key == "." || key == ".." || key == "" {
		return "unnamed"
	}
	if len(key) > maxKey {
		h := fnv.New32a()
		_, _ = h.Write([]byte(key))
		key = fmt.Sprintf("%s-%08x", key[:maxKey], h.Sum32())
	}
	return key
}

// tmuxSessionName is the name of the tmux session this process runs in, or
// "" outside tmux. tmux resolves the session from $TMUX, so this is the
// session that owns the calling pane, not whichever client is focused.
func tmuxSessionName() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#S").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// tmuxOption reads a tmux option for the current session, or "" when this
// process is not in tmux, tmux is absent, or the option is unset.
func tmuxOption(name string) string {
	if os.Getenv("TMUX") == "" {
		return ""
	}
	out, err := exec.Command("tmux", "show-option", "-qv", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
