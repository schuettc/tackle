# proj Rewrite — Phase 2 (Rich State) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Turn the Phase-1 picker into the agent-aware live dashboard the spec describes: fix agent **state** so working/waiting actually fire, add **git status**, add the feature-detected **muster ✉ attention** column, replace the stub **preview pane** with the rich version (agent · muster · git · last line), and add the **live-refresh tick**.

**Architecture:** All new reads live in `internal/proj` (pure/tmux/git/muster helpers, each degrading to zero values). `internal/projtui` consumes them: rows gain a ✉ count, the preview pane computes expensive fields **lazily for the highlighted row only**, and a Bubble Tea `tea.Tick` re-runs the cheap discovery on an interval.

**Tech Stack:** Go 1.26, Bubble Tea, tmux, git.

**Spec:** `tackle/docs/superpowers/specs/2026-08-29-proj-rewrite-design.md` — "Row + preview state model", "muster is optional — a hard guarantee". Builds on Phase 1 (`cmd/proj`, `internal/proj`, `internal/projtui`).

## Global Constraints

- **muster is optional — hard guarantee (verbatim from spec):** proj does NOT import muster. The muster read is a runtime feature-detect: shell `muster status --json` only if `muster` is on `PATH`; on absence/error/parse-failure the ✉ column and its preview line simply do not render. Never error, never block.
- **Side-effect-free muster read:** use `muster status --json`, NOT `muster inbox <alias>` (which journals a peek). The command is provisional (requested from the muster session, thread 335); parse tolerantly and degrade if the shape differs.
- **Cheap vs expensive (perf):** every picker tick computes only cheap fields for ALL rows (agent presence/state, ✉ count). Expensive fields (git, last-line) are computed ONLY for the highlighted row's preview, on demand.
- **Degradation everywhere:** git not a repo → no git line; muster absent → no ✉; unknown agent → "shell"/idle. Zero values, never errors.
- **Testing:** pure logic (git parse, muster parse, state classify) = unit tests, no external processes (feed fixture strings to pure parsers). tmux/git integration = throwaway socket / `t.TempDir()` git repo, skip when the tool is absent. Bubble Tea = `Update()`-driven. Run `gofmt -l . && go vet ./... && go build ./... && go test -race ./...` before each commit.

---

## File Structure

```
internal/proj/
  agentstate.go   (MODIFY)  AgentIn queries the MAIN PANE for activity/bell; classifyState pure helper
  agentstate_test.go (MOD)  add TestClassifyState
  git.go          (NEW)     GitStatus(dir) → GitInfo{Branch,Ahead,Behind,Dirty}
  git_test.go     (NEW)     integration on a t.TempDir() repo
  muster.go       (NEW)     MusterCounts() map[alias]Attention, feature-detected; parseMusterStatus pure
  muster_test.go  (NEW)     parse fixtures + absent-binary degradation
  discovery.go    (MODIFY)  Session gains Unread/ActionReq; LiveSessions maps counts by alias
internal/projtui/
  model.go        (MODIFY)  Row gains Unread/ActionReq; tickMsg + tea.Tick refresh; New() shares a Sources hook for testable refresh
  view.go         (MODIFY)  rows render ✉N; previewPane rich (agent/muster/git/last-line), lazy
  model_test.go   (MOD)     TestTickRefreshPreservesSelection; ✉ render test
```

---

### Task 1: Fix agent state — query the main pane, not the session

**Files:** Modify `internal/proj/agentstate.go`, `internal/proj/agentstate_test.go`

**Why:** Phase 1's `AgentIn` queried `#{pane_activity}`/`#{window_bell_flag}` against the *session* target, so state degraded to "idle". `pane_activity` is a pane property; it must be read from the resolved main pane. Also `#{pane_activity}` is an epoch timestamp, so "recent activity" needs comparison to now, not `!= "0"`.

**Interfaces:**
- Produces: `paneMain(socket, session) (paneID string, activity int64)` — returns the main pane's id + its `pane_activity` epoch; `classifyState(kind string, bellFlag bool, activityAgeSecs int64) string` (pure) — {working,waiting,idle}; `AgentIn` unchanged signature, now correct.

- [ ] **Step 1: Unit-test the pure state classifier**

Add to `agentstate_test.go`:
```go
func TestClassifyState(t *testing.T) {
	cases := []struct {
		kind    string
		bell    bool
		ageSecs int64
		want    string
	}{
		{"pi", true, 999, "waiting"},        // bell wins
		{"pi", false, 3, "working"},         // recent activity
		{"claude", false, 3600, "idle"},     // stale activity
		{"shell", false, 1, "idle"},         // shells never work
		{"", false, 1, ""},                  // unknown stays empty
	}
	for _, c := range cases {
		if got := classifyState(c.kind, c.bell, c.ageSecs); got != c.want {
			t.Errorf("classifyState(%q,%v,%d)=%q want %q", c.kind, c.bell, c.ageSecs, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run — confirm undefined `classifyState`.**

- [ ] **Step 3: Rewrite the state logic in `agentstate.go`.** Replace the `AgentIn` body's state section:

```go
const activeWithinSecs = 30 // pane_activity newer than this ⇒ "working"

// classifyState maps (kind, bell, activity-age) to a coarse state.
func classifyState(kind string, bell bool, ageSecs int64) string {
	if kind == "" {
		return ""
	}
	if kind == "shell" {
		return "idle"
	}
	switch {
	case bell:
		return "waiting"
	case ageSecs >= 0 && ageSecs <= activeWithinSecs:
		return "working"
	default:
		return "idle"
	}
}

// AgentIn returns the agent kind and coarse state of session's main pane.
func AgentIn(socket, session string) (kind, state string) {
	paneID, cmd, activity := paneMain(socket, session)
	kind = classifyAgent(cmd)
	if kind == "" || kind == "shell" {
		return kind, classifyState(kind, false, -1)
	}
	bell := Query(socket, paneID, "#{window_bell_flag}") == "1"
	age := int64(-1)
	if activity > 0 {
		age = time.Now().Unix() - activity
	}
	return kind, classifyState(kind, bell, age)
}

// paneMain returns the main pane's id, current command, and pane_activity
// epoch — the leftmost pane, ties broken by tallest.
func paneMain(socket, session string) (id, cmd string, activity int64) {
	out, err := Run(socket, "list-panes", "-t", session, "-F",
		"#{pane_left} #{pane_height} #{pane_id} #{pane_activity} #{pane_current_command}")
	if err != nil {
		return "", "", 0
	}
	bestLeft, bestH := 1<<30, -1
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 5 {
			continue
		}
		l, h := atoi(f[0]), atoi(f[1])
		if l < bestLeft || (l == bestLeft && h > bestH) {
			bestLeft, bestH = l, h
			id, activity, cmd = f[2], int64(atoi(f[3])), f[4]
		}
	}
	return id, cmd, activity
}
```
Remove the now-unused `paneMainCommand` (its callers, if any in tests, switch to `paneMain`). Add `"time"` to imports. Note: `window_bell_flag` is a window property; querying it via the pane id resolves the pane's window — correct. Keep `classifyAgent`/`looksLikeVersion` unchanged.

- [ ] **Step 4: Run tests + gates.** `go test -race ./internal/proj/` (TestClassifyState + existing TestClassifyAgent pass; integration TestServersAndFind still green).

- [ ] **Step 5: Commit** — `feat(proj): agent state reads the main pane (working/waiting fire)`.

---

### Task 2: git status helper

**Files:** Create `internal/proj/git.go`, `internal/proj/git_test.go`

**Interfaces:**
- Produces: `type GitInfo struct { Repo bool; Branch string; Ahead, Behind, Dirty int }`; `func GitStatus(dir string) GitInfo` — runs `git -C dir status --porcelain=v2 --branch`; `Repo=false` (zero value) if `dir` is not a git repo or git is absent. Never errors.

- [ ] **Step 1: Integration test** (`git_test.go`, skip if no git)

```go
func TestGitStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil { t.Skip("no git") }
	dir := t.TempDir()
	run := func(a ...string) { exec.Command("git", append([]string{"-C", dir}, a...)...).Run() }
	run("init", "-b", "main")
	run("config", "user.email", "t@t"); run("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	run("add", "."); run("commit", "-m", "one")
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y"), 0o644) // untracked ⇒ dirty

	g := GitStatus(dir)
	if !g.Repo || g.Branch != "main" { t.Fatalf("git=%+v", g) }
	if g.Dirty != 1 { t.Fatalf("dirty=%d want 1", g.Dirty) }

	if GitStatus(t.TempDir()).Repo { t.Fatal("non-repo must report Repo=false") }
}
```

- [ ] **Step 2: Run — confirm failure.**

- [ ] **Step 3: Implement `git.go`.**

```go
package proj

import (
	"os/exec"
	"strconv"
	"strings"
)

type GitInfo struct {
	Repo           bool
	Branch         string
	Ahead, Behind  int
	Dirty          int
}

// GitStatus reports the branch, ahead/behind, and dirty-file count for dir.
// Zero value (Repo=false) when dir is not a git work tree or git is absent.
func GitStatus(dir string) GitInfo {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain=v2", "--branch").Output()
	if err != nil {
		return GitInfo{}
	}
	g := GitInfo{Repo: true}
	for _, ln := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(ln, "# branch.head "):
			g.Branch = strings.TrimPrefix(ln, "# branch.head ")
		case strings.HasPrefix(ln, "# branch.ab "):
			// "# branch.ab +A -B"
			f := strings.Fields(ln)
			if len(f) == 4 {
				g.Ahead = abs(atoiSigned(f[2]))
				g.Behind = abs(atoiSigned(f[3]))
			}
		case ln == "":
		case ln[0] == '#':
		default:
			g.Dirty++ // 1/2/u/? entries are all working-tree changes
		}
	}
	if g.Branch == "(detached)" {
		g.Branch = "detached"
	}
	return g
}

func atoiSigned(s string) int { n, _ := strconv.Atoi(strings.TrimPrefix(s, "+")); return n }
func abs(n int) int { if n < 0 { return -n }; return n }
```

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit** — `feat(proj): git status helper (branch/ahead/behind/dirty)`.

---

### Task 3: muster attention reader (feature-detected, side-effect-free)

**Files:** Create `internal/proj/muster.go`, `internal/proj/muster_test.go`

**Interfaces:**
- Produces:
  - `type Attention struct { Unread, ActionRequired int }`
  - `func MusterCounts() map[string]Attention` — `""`-safe map keyed by alias; empty map if `muster` absent, errors, or output unparsable. Runs `muster status --json`.
  - `func parseMusterStatus(b []byte) map[string]Attention` (pure) — tolerant parse of the provisional shape `[{"alias":"...","unread":N,"action_required":N}, ...]`; also accepts a top-level `{"agents":[...]}` wrapper. Unknown shape → empty map.

- [ ] **Step 1: Unit tests over fixtures**

```go
func TestParseMusterStatus(t *testing.T) {
	arr := []byte(`[{"alias":"tw/tackle","unread":3,"action_required":1},{"alias":"tw/muster","unread":0}]`)
	m := parseMusterStatus(arr)
	if m["tw/tackle"].Unread != 3 || m["tw/tackle"].ActionRequired != 1 { t.Fatalf("%+v", m) }
	if m["tw/muster"].Unread != 0 { t.Fatalf("%+v", m) }

	wrapped := []byte(`{"agents":[{"alias":"a/b","unread":2,"action_required":0}]}`)
	if parseMusterStatus(wrapped)["a/b"].Unread != 2 { t.Fatal("wrapper shape") }

	if len(parseMusterStatus([]byte("not json"))) != 0 { t.Fatal("garbage → empty") }
	if len(parseMusterStatus([]byte(`{"weird":1}`)) ) != 0 { t.Fatal("unknown shape → empty") }
}

func TestMusterCountsAbsentBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no muster on PATH
	if len(MusterCounts()) != 0 { t.Fatal("absent muster → empty map, no error") }
}
```

- [ ] **Step 2: Run — confirm failure.**

- [ ] **Step 3: Implement `muster.go`.**

```go
package proj

import (
	"encoding/json"
	"os/exec"
)

type Attention struct {
	Unread, ActionRequired int
}

type musterRow struct {
	Alias          string `json:"alias"`
	Unread         int    `json:"unread"`
	ActionRequired int    `json:"action_required"`
}

// MusterCounts returns per-alias attention counts, or an empty map when muster
// is not installed / errors / emits an unrecognized shape. Side-effect-free:
// `muster status --json` must not journal a peek.
func MusterCounts() map[string]Attention {
	if _, err := exec.LookPath("muster"); err != nil {
		return map[string]Attention{}
	}
	out, err := exec.Command("muster", "status", "--json").Output()
	if err != nil {
		return map[string]Attention{}
	}
	return parseMusterStatus(out)
}

func parseMusterStatus(b []byte) map[string]Attention {
	m := map[string]Attention{}
	var arr []musterRow
	if json.Unmarshal(b, &arr) == nil && len(arr) > 0 {
		for _, r := range arr {
			if r.Alias != "" {
				m[r.Alias] = Attention{r.Unread, r.ActionRequired}
			}
		}
		return m
	}
	var wrap struct {
		Agents []musterRow `json:"agents"`
	}
	if json.Unmarshal(b, &wrap) == nil {
		for _, r := range wrap.Agents {
			if r.Alias != "" {
				m[r.Alias] = Attention{r.Unread, r.ActionRequired}
			}
		}
	}
	return m
}
```

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit** — `feat(proj): feature-detected muster attention reader`.

---

### Task 4: Wire attention into discovery + rows

**Files:** Modify `internal/proj/discovery.go`, `internal/projtui/model.go`, `internal/projtui/model_test.go`

**Interfaces:**
- Consumes: `MusterCounts`, `AliasFor`, `LabelOption`.
- Produces: `Session` gains `Unread, ActionRequired int`. `LiveSessions()` calls `MusterCounts()` ONCE, then for each session computes its alias (`AliasFor(socket, label)` where `label = Query(socket, name, "#{@claude_task}")`) and attaches the counts. `Row` gains `Unread, ActionRequired int`; `New()` copies them onto session rows.

- [ ] **Step 1: Extend `Session` + `LiveSessions`** (discovery.go): add `Unread, ActionRequired int` to `Session`; after building the base list, `counts := MusterCounts()`, and for each session set `label := Query(sock, name, "#{"+LabelOption()+"}")`, `a := counts[AliasFor(sock, label)]`, `s.Unread, s.ActionRequired = a.Unread, a.ActionRequired`.

- [ ] **Step 2: Extend `Row` + `New`** (model.go): add `Unread, ActionRequired int` to `Row`; in `New()` where session rows are built, copy `Unread: s.Unread, ActionRequired: s.ActionRequired`.

- [ ] **Step 3: Test the row carries the count** (model_test.go): a fixture-injected session row with `Unread:3` renders `✉3` (assert via the row-render helper or a `View()` substring). Since `LiveSessions` needs tmux+muster, test at the Row level: build a `Model` with a session Row `{Unread:3}` and assert `View()` contains `✉3`. (Rendering added in Task 5's view change — if that isn't in yet, assert the Row field is populated by a small helper; the ✉ render assertion can live with Task 5. Keep this task's test at the data level: `New` copies the field.) Prefer a pure test: call the row-building path with a fixture `[]Session` if `New` is refactored to accept sources (see Task 6); otherwise assert the struct copy directly.

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit** — `feat(proj): attach muster attention to sessions + rows`.

---

### Task 5: Render ✉ on rows + the rich preview pane

**Files:** Modify `internal/projtui/view.go`, `internal/projtui/model_test.go`

**Interfaces:**
- Consumes: `Row.Unread/ActionRequired`, `proj.GitStatus`, `proj.AgentIn` (already on rows), the highlighted row.
- Produces: session rows render `● name  agent·state  ✉N` (✉ only when `Unread>0`; a `!` marker when `ActionRequired>0`). `previewPane` becomes rich: for the highlighted **session** row it shows agent + state, `✉N unread · M action-required` (only if >0), and `git branch ↑A ↓B ●D` (only if `GitStatus(row.Dir).Repo`); for a **project** row, the project dir + its git status. Expensive fields (`GitStatus`) computed HERE, lazily, only for the highlighted row.

- [ ] **Step 1: Row-render test** — a `Model` whose highlighted... actually test the unselected render: build a Model with a session Row `{Label:"p/w", Agent:"pi", State:"working", Unread:2}`; assert `View()` contains `✉2` and `pi` and `working`. Add `TestRowShowsAttention`.

- [ ] **Step 2: Preview test** — highlight a session row with `Dir` = a `t.TempDir()` git repo (init+commit as in git_test), assert `View()` preview contains the branch name. Guard with git-present skip. Add `TestPreviewShowsGit`.

- [ ] **Step 3: Implement the view changes.** Update the row builder to append `"  ✉"+N` when `Unread>0` (and a `"!"` when `ActionRequired>0`), matching the existing lipgloss styles (reuse `colMauve`/a new amber style for ✉). Rewrite `previewPane` (replace the Phase-1 stub at view.go:110-129): compute `g := proj.GitStatus(r.Dir)` for the highlighted row, render head + agent line + attention line (if any) + git line (if `g.Repo`) + dir. Keep it defensive: empty `r.Dir` → skip git.

- [ ] **Step 4: Run tests + gates.**

- [ ] **Step 5: Commit** — `feat(proj): render ✉ attention + rich preview pane (agent/muster/git)`.

---

### Task 6: Live-refresh tick

**Files:** Modify `internal/projtui/model.go`, `internal/projtui/model_test.go`

**Interfaces:**
- Produces: a `tickMsg` and a `tea.Tick` command started in `Init()` firing every `refreshInterval = 1500 * time.Millisecond`; on `tickMsg`, re-run cheap discovery (`proj.LiveSessions` + `proj.AllProjectDirs`), rebuild the session/project rows, **preserve** the current view, filter, cursor position (clamped), and any in-progress text input, then re-arm the tick. To keep this testable, factor the row (re)build behind a `sources` function field on `Model` (default = the real `proj.*` calls; tests inject fixtures).

- [ ] **Step 1: Refactor `New`/`newModel` to hold a `refresh func() (sessions, projects []Row)`** field (default wraps the real `proj.LiveSessions`/`AllProjectDirs`→Row conversion; extract that conversion into a helper so both `New` and the tick use it). No behavior change yet.

- [ ] **Step 2: Test tick preserves selection** (`model_test.go`):
```go
func TestTickRefreshPreservesSelection(t *testing.T) {
	m := newTestModelWithRefresh(/* initial rows */, func() (s, p []Row) {
		return []Row{{Kind: RowSession, Label: "p/w2", Name: "p/w2"}, {Kind: RowSession, Label: "p/w1", Name: "p/w1"}}, nil
	})
	m.cursor = 1
	next, _ := m.Update(tickMsg{})
	m = next.(Model)
	// cursor stays in-bounds; filter/view unchanged; rows refreshed
	if m.cursor < 0 || m.cursor >= len(m.visibleRows()) { t.Fatal("cursor not clamped after refresh") }
	if m.view != viewEntrance { t.Fatal("view changed on tick") }
}
```
(Use whatever the existing test shims expose — `visibleRows`/`viewEntrance` names may differ; match the actual model.)

- [ ] **Step 3: Implement `Init()` returning `tea.Batch(textinput.Blink?, tick())`** where `tick()` is `tea.Tick(refreshInterval, func(time.Time) tea.Msg { return tickMsg{} })`; handle `tickMsg` in `Update` (rebuild rows via `m.refresh`, preserve view/filter/cursor-clamped/input, return `m, tick()`). Do NOT refresh while `m.inputting` is true (don't disturb a name being typed) — just re-arm the tick.

- [ ] **Step 4: Run tests + gates** (`go test -race ./...`, whole module).

- [ ] **Step 5: Commit** — `feat(proj): live-refresh tick preserving selection`.

---

## Deferred to later phases

- **Phase 3:** the sidebar builder (Go), scratch no-probe mode, defenses.
- **Phase 4:** the `proj()` shim + auto-join hook + the agent skill; the zsh cutover.
- **muster shape reconciliation:** if the muster session ships `muster status --json` with a different field/shape than the provisional one, adjust `parseMusterStatus` to match (small, isolated).

## Self-Review

- **Spec coverage:** agent-state main-pane fix (T1, the Phase-1 deferral); git (T2); muster ✉ feature-detected + side-effect-free (T3, T4); rich preview lazy (T5); live tick (T6). All "Row + preview state model" rows sourced.
- **Placeholder scan:** none. The muster shape is explicitly provisional with a tolerant parser + degradation, not a placeholder. Test shim names (`visibleRows`/`viewEntrance`/`newTestModelWithRefresh`) are flagged "match the actual model" because they must align with Phase-1's existing test helpers — the implementer reads model_test.go and uses the real names.
- **Type consistency:** `Attention{Unread,ActionRequired}` (T3) → `Session.Unread/ActionRequired` (T4) → `Row.Unread/ActionRequired` (T4) → rendered (T5). `GitInfo` (T2) consumed by the preview (T5). `classifyState` (T1) is internal to agentstate. `refresh func()` (T6) wraps the Row conversion shared with `New`.
