package notes

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAmbient builds an ambient from plain maps so a test can describe the
// environment a scratch invocation would see without touching the real one.
func fakeAmbient(env map[string]string, tmux map[string]string, cfg string, cfgErr error) ambient {
	return ambient{
		getenv:      func(k string) string { return env[k] },
		tmuxOpt:     func(k string) string { return tmux[k] },
		tmuxSession: func() string { return tmux[tmuxSessionKey] },
		configDir: func() (string, error) {
			if cfgErr != nil {
				return "", cfgErr
			}
			return cfg, nil
		},
	}
}

// tmuxSessionKey is the fake-tmux map slot that stands in for `#S`.
const tmuxSessionKey = "#S"

func padsDir(cfg string) string { return filepath.Join(cfg, "scratch", "pads") }

func TestResolvePrefersExplicitFile(t *testing.T) {
	a := fakeAmbient(
		map[string]string{EnvFile: "/tmp/pinned.md", EnvClaudeSession: "sess-1", EnvDir: "/tmp/store"},
		map[string]string{TmuxOption: "sess-2"}, "/cfg", nil)
	got, err := resolve("/work", a)
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	if got != "/tmp/pinned.md" {
		t.Fatalf("resolve() = %q, want the pinned file", got)
	}
}

func TestResolveKeysOnAgentSession(t *testing.T) {
	a := fakeAmbient(map[string]string{EnvClaudeSession: "abc-123"}, nil, "/cfg", nil)
	got, err := resolve("/work/repo", a)
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	want := filepath.Join(padsDir("/cfg"), "abc-123.md")
	if got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

// AGENT_SESSION_ID is the harness-neutral identifier (pi exports it into
// every subprocess); it keys the pad exactly like the Claude-specific var.
func TestResolveKeysOnGenericAgentSession(t *testing.T) {
	a := fakeAmbient(map[string]string{EnvAgentSession: "01a0-pi-sess"}, nil, "/cfg", nil)
	got, err := resolve("/work/repo", a)
	if err != nil {
		t.Fatalf("resolve() error = %v", err)
	}
	want := filepath.Join(padsDir("/cfg"), "01a0-pi-sess.md")
	if got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

// When both are present the generic var wins: a Claude process spawned
// INSIDE another agent's session (pi's claude-bridge children) carries its
// own CLAUDE_CODE_SESSION_ID plus the outer agent's AGENT_SESSION_ID, and
// the pad belongs to the outer conversation.
func TestResolveGenericAgentSessionBeatsClaude(t *testing.T) {
	a := fakeAmbient(map[string]string{
		EnvAgentSession:  "outer-agent",
		EnvClaudeSession: "bridge-child",
	}, nil, "/cfg", nil)
	got, _ := resolve("/work", a)
	if want := filepath.Join(padsDir("/cfg"), "outer-agent.md"); got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

// The pad follows the conversation: same session id from a different
// directory is the same pad. This is what makes a resumed session in a new
// pane or worktree reopen its own notes.
func TestResolveSameSessionDifferentDirs(t *testing.T) {
	a := fakeAmbient(map[string]string{EnvClaudeSession: "abc-123"}, nil, "/cfg", nil)
	one, _ := resolve("/work/repo", a)
	two, _ := resolve("/somewhere/else/entirely", a)
	if one != two {
		t.Fatalf("pad moved with cwd: %q vs %q", one, two)
	}
}

// The regression that motivated the store: two agent sessions in ONE
// checkout must not share a pad.
func TestResolveTwoSessionsOneDirDiffer(t *testing.T) {
	dir := "/work/shared-checkout"
	one, _ := resolve(dir, fakeAmbient(map[string]string{EnvClaudeSession: "sess-a"}, nil, "/cfg", nil))
	two, _ := resolve(dir, fakeAmbient(map[string]string{EnvClaudeSession: "sess-b"}, nil, "/cfg", nil))
	if one == two {
		t.Fatalf("both sessions resolved to %q", one)
	}
}

// A sibling pane cannot inherit the agent's environment, so the tmux option
// is the only way it learns the session — but a real env var wins when both
// are present.
func TestResolveFallsBackToTmuxOption(t *testing.T) {
	a := fakeAmbient(nil, map[string]string{TmuxOption: "tmux-sess"}, "/cfg", nil)
	got, _ := resolve("/work", a)
	if want := filepath.Join(padsDir("/cfg"), "tmux-sess.md"); got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

func TestResolveEnvBeatsTmux(t *testing.T) {
	a := fakeAmbient(map[string]string{EnvClaudeSession: "from-env"},
		map[string]string{TmuxOption: "from-tmux"}, "/cfg", nil)
	got, _ := resolve("/work", a)
	if want := filepath.Join(padsDir("/cfg"), "from-env.md"); got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

// No agent, but inside a named tmux session: the pad belongs to that tmux
// session, so two shell-only sessions in one checkout do not share a pad.
func TestResolveNoAgentUsesTmuxSessionName(t *testing.T) {
	a := fakeAmbient(nil, map[string]string{tmuxSessionKey: "proj/html"}, "/cfg", nil)
	got, _ := resolve("/work", a)
	if want := filepath.Join(padsDir("/cfg"), "proj-html.md"); got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

func TestResolveTwoTmuxSessionsOneDirDiffer(t *testing.T) {
	one, _ := resolve("/work", fakeAmbient(nil, map[string]string{tmuxSessionKey: "proj/html"}, "/cfg", nil))
	two, _ := resolve("/work", fakeAmbient(nil, map[string]string{tmuxSessionKey: "proj/admin-page"}, "/cfg", nil))
	if one == two {
		t.Fatalf("both sessions resolved to %q", one)
	}
}

func TestResolveAgentBeatsTmuxSessionName(t *testing.T) {
	a := fakeAmbient(nil, map[string]string{TmuxOption: "agent-id", tmuxSessionKey: "proj/html"}, "/cfg", nil)
	got, _ := resolve("/work", a)
	if want := filepath.Join(padsDir("/cfg"), "agent-id.md"); got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

func TestResolveNoSessionUsesDirKey(t *testing.T) {
	a := fakeAmbient(nil, nil, "/cfg", nil)
	got, _ := resolve("/Users/c/dotfiles", a)
	if !strings.HasPrefix(got, padsDir("/cfg")) {
		t.Fatalf("resolve() = %q, want it inside the store", got)
	}
	if strings.Contains(got, "dotfiles/") || strings.HasSuffix(got, "/dotfiles/.scratch.md") {
		t.Fatalf("resolve() = %q, want a flattened key, not a nested path", got)
	}
	if !strings.Contains(filepath.Base(got), "Users-c-dotfiles") {
		t.Fatalf("resolve() base = %q, want the directory flattened into it", filepath.Base(got))
	}
}

// Two different directories with no agent session must not collide.
func TestResolveDistinctDirsDistinctPads(t *testing.T) {
	a := fakeAmbient(nil, nil, "/cfg", nil)
	one, _ := resolve("/work/alpha", a)
	two, _ := resolve("/work/beta", a)
	if one == two {
		t.Fatalf("both dirs resolved to %q", one)
	}
}

func TestResolveHonorsStoreOverride(t *testing.T) {
	a := fakeAmbient(map[string]string{EnvDir: "/custom/store", EnvClaudeSession: "s1"}, nil, "/cfg", nil)
	got, _ := resolve("/work", a)
	if want := filepath.Join("/custom/store", "pads", "s1.md"); got != want {
		t.Fatalf("resolve() = %q, want %q", got, want)
	}
}

// No store location and no override is a real error, not a silent write
// into the working directory — landing a pad in the repo is the exact
// outcome this design removes.
func TestResolveErrorsWhenStoreUnknown(t *testing.T) {
	a := fakeAmbient(nil, nil, "", errors.New("no home"))
	got, err := resolve("/work/repo", a)
	if err == nil {
		t.Fatalf("resolve() = %q, want an error", got)
	}
	if got != "" {
		t.Fatalf("resolve() = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), EnvDir) {
		t.Fatalf("error %q should name %s as the way out", err, EnvDir)
	}
}

// A session id is attacker-adjacent input: it arrives from the environment
// or a tmux option. It must not be able to place the pad outside the store.
func TestSafeKeyContainsTraversal(t *testing.T) {
	for _, in := range []string{"../../etc/passwd", "..", ".", "", "a/b", `a\b`, "a:b", "a b"} {
		got := safeKey(in)
		if strings.ContainsAny(got, `/\:`) {
			t.Fatalf("safeKey(%q) = %q, still contains a separator", in, got)
		}
		if got == "." || got == ".." || got == "" {
			t.Fatalf("safeKey(%q) = %q, still traverses", in, got)
		}
	}
}

func TestSafeKeyTraversalStaysInStore(t *testing.T) {
	a := fakeAmbient(map[string]string{EnvClaudeSession: "../../../../etc/passwd"}, nil, "/cfg", nil)
	got, _ := resolve("/work", a)
	if !strings.HasPrefix(filepath.Clean(got), filepath.Clean(padsDir("/cfg"))) {
		t.Fatalf("resolve() = %q, escaped the store", got)
	}
}

// Deep directories must not produce a filename the filesystem rejects.
func TestSafeKeyBoundsLength(t *testing.T) {
	long := "/" + strings.Repeat("verylongsegment/", 60)
	a := fakeAmbient(nil, nil, "/cfg", nil)
	got, _ := resolve(long, a)
	if base := filepath.Base(got); len(base) > maxKey+16 {
		t.Fatalf("base name is %d chars, want bounded near %d", len(base), maxKey)
	}
}

// Truncation must not collapse two different long paths into one pad.
func TestSafeKeyLongPathsStayDistinct(t *testing.T) {
	prefix := "/" + strings.Repeat("segment/", 60)
	a := fakeAmbient(nil, nil, "/cfg", nil)
	one, _ := resolve(prefix+"alpha", a)
	two, _ := resolve(prefix+"beta", a)
	if one == two {
		t.Fatalf("distinct long paths collided at %q", one)
	}
}
