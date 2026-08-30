package proj

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// sidebarSettle is how long a probing app (scratch/yazi) is left focused after
// its pane is created, so it can read the terminal responses (bg-color / cursor
// position) tmux delivers to the FOCUSED pane. Ported from the bash builder's
// `sleep 0.5` between the probing splits. Without it those responses leak into
// the next pane as escape-code garbage.
const sidebarSettle = 500 * time.Millisecond

// resolveDir validates dir and, when it is gone, falls back the way the bash
// builder does: nearest surviving ancestor (stay near the work), else the
// session's own #{session_path}, else $HOME. Callers cannot guarantee dir still
// exists — prefix f passes #{pane_current_path}, a dead path the moment the
// git worktree you were sitting in is deleted. An unusable dir fails invisibly
// (yazi PANICs when its cwd is gone, its pane vanishes, and the chained splits
// then target a dead pane), so we never return empty.
func resolveDir(socket, session, dir string) string {
	if dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return dir
		}
	}
	// Walk up to the nearest surviving ancestor. Only meaningful for absolute
	// paths; a relative/empty dir has nothing to walk (filepath.Dir bottoms out
	// at ".") and drops through to the session_path/$HOME fallbacks.
	if filepath.IsAbs(dir) {
		d := dir
		for {
			parent := filepath.Dir(d)
			if parent == d {
				break // reached "/"
			}
			d = parent
			if fi, err := os.Stat(d); err == nil && fi.IsDir() {
				return d
			}
		}
	}
	if sp := Query(socket, session, "#{session_path}"); sp != "" {
		if fi, err := os.Stat(sp); err == nil && fi.IsDir() {
			return sp
		}
	}
	return os.Getenv("HOME")
}

// shellBin returns the user's login shell, or zsh as the bash builder's
// ${SHELL:-zsh} default.
func shellBin() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "zsh"
}

// appCommand builds the `cd <qdir> && exec <app>` string each pane runs. Each
// pane cd's to dir itself instead of trusting tmux's -c: a tmux server whose
// own cwd was deleted silently ignores -c and births every pane in the dead
// path. -c is still passed for the healthy case; this is the belt to its
// braces. A missing binary degrades to a shell rather than breaking the chain
// (a dead split target would fail every subsequent, best-effort split).
func appCommand(app, dir string) string {
	var run string
	switch app {
	case "scratch":
		run = "scratch"
		if _, err := exec.LookPath("scratch"); err != nil {
			run = shellBin()
		}
	case "shell":
		run = shellBin()
	default:
		run = app
		if _, err := exec.LookPath(app); err != nil {
			run = shellBin()
		}
	}
	return fmt.Sprintf("cd %s && exec %s", shellQuote(dir), run)
}

// mainPane returns the leftmost (tie: tallest) pane id of session. Leftmost is
// the main (left) pane; the tallest tie-break is robust to whatever agent panes
// exist to its right. A `list-panes | head -1` would break once pane indices
// renumber after kills.
func mainPane(socket, session string) string {
	out, err := Run(socket, "list-panes", "-t", session, "-F", "#{pane_left} #{pane_height} #{pane_id}")
	if err != nil {
		return ""
	}
	best, bestLeft, bestHeight := "", 1<<30, -1
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		l, h := atoi(f[0]), atoi(f[1])
		if l < bestLeft || (l == bestLeft && h > bestHeight) {
			bestLeft, bestHeight, best = l, h, f[2]
		}
	}
	return best
}

// anchorPane returns the topmost (tie: leftmost) non-main, non-@sidebar pane
// id, or "". This is the top of the agent column left behind when the sidebar
// was toggled off while agent subagents were running. The -t session target is
// load-bearing: untargeted, list-panes reads the server's CURRENT session, so
// the column could build inside the wrong session.
func anchorPane(socket, session, main string) string {
	out, err := Run(socket, "list-panes", "-t", session, "-F", "#{pane_id} #{pane_top} #{pane_left} #{?@sidebar,1,0}")
	if err != nil {
		return ""
	}
	best, bestTop, bestLeft := "", 1<<30, 1<<30
	for _, ln := range splitLines(out) {
		f := strings.Fields(ln)
		if len(f) < 4 || f[0] == main || f[3] == "1" {
			continue
		}
		tp, lf := atoi(f[1]), atoi(f[2])
		if tp < bestTop || (tp == bestTop && lf < bestLeft) {
			bestTop, bestLeft, best = tp, lf, f[0]
		}
	}
	return best
}

// splitTag runs a split-window with the given split flags off target, running
// app's command via `cd && exec`, then tags the new pane `@sidebar 1` so the
// sidebar-toggle can find the whole column by tag regardless of what runs in
// it. Returns the new pane id, or "" on failure. Every tmux call is
// best-effort (mirrors the bash's 2>/dev/null || return 0).
func splitTag(socket, target, dir, app string, splitFlags ...string) string {
	args := []string{"split-window"}
	args = append(args, splitFlags...)
	args = append(args, "-t", target, "-c", dir, "-P", "-F", "#{pane_id}", appCommand(app, dir))
	id, err := Run(socket, args...)
	if err != nil || id == "" {
		return ""
	}
	_, _ = Run(socket, "set-option", "-p", "-t", id, "@sidebar", "1")
	return id
}

// applySizes resizes each built pane whose app name has a size (in rows) in the
// layout, mirroring the bash's `resize-pane -y 12` (scratch) / `-y 10` (shell).
// ids is parallel to panes. Best-effort.
func applySizes(socket string, panes, ids []string, sizes map[string]int) {
	if len(sizes) == 0 {
		return
	}
	for i, app := range panes {
		if i >= len(ids) || ids[i] == "" {
			continue
		}
		if rows, ok := sizes[app]; ok {
			_, _ = Run(socket, "resize-pane", "-t", ids[i], "-y", fmt.Sprintf("%d", rows))
		}
	}
}

// BuildSidebar builds the standard right column for session: each pane in
// layout.Panes stacked in a 30%-wide column, every pane tagged @sidebar 1.
// It ports proj-right-column.sh with all six of its failure-mode defenses:
//  1. resolveDir dead-path fallback (validate dir ONCE, here).
//  2. mainPane = leftmost, tallest tie-break.
//  3. anchor detection: insert ABOVE the agent column (-v -b) if agents are
//     present, else split the 30% column off main (-h -l 30%).
//  4. @agent_pin suppress → set 0 → build → restore → re-assert main -x 70%
//     (when pinned & >=3 panes) → select main. Done via defer so it always
//     runs. The pin hook's after-split resize would otherwise crush every
//     fresh sidebar pane to 1 column.
//  5. each pane cd <qdir> && exec <app> (belt to -c's braces).
//  6. @sidebar 1 tag on every built pane.
//
// Probe-stagger: scratch/yazi panes are left focused for ~sidebarSettle so they
// can read their startup terminal probes. Best-effort throughout; returns
// nothing and logs nothing.
func BuildSidebar(socket, session, dir string, layout Layout) {
	panes := layout.Panes
	if len(panes) == 0 {
		panes = []string{"scratch", "yazi", "shell"}
	}
	dir = resolveDir(socket, session, dir)
	main := mainPane(socket, session)
	if main == "" {
		return
	}

	prevPin, _ := Run(socket, "show-option", "-gqv", "@agent_pin")
	_, _ = Run(socket, "set-option", "-g", "@agent_pin", "0")
	defer func() {
		pin := prevPin
		if pin == "" {
			pin = "1"
		}
		_, _ = Run(socket, "set-option", "-g", "@agent_pin", pin)
		if pin == "1" {
			if wc := Query(socket, session, "#{window_panes}"); atoi(wc) >= 3 {
				_, _ = Run(socket, "resize-pane", "-t", main, "-x", "70%")
			}
		}
		_, _ = Run(socket, "select-pane", "-t", main)
	}()

	anchor := anchorPane(socket, session, main)
	ids := make([]string, len(panes))
	var prev string
	for i, app := range panes {
		var id string
		switch {
		case i == 0 && anchor != "":
			// Agents occupy the right column. Insert the stack ABOVE them,
			// in-column (-b); width is inherited so no -l. The agent-pin
			// re-assert restores main to 70%.
			id = splitTag(socket, anchor, dir, app, "-v", "-b")
		case i == 0:
			// No foreign panes: split the 30% column off the right of main.
			id = splitTag(socket, main, dir, app, "-h", "-l", "30%")
		default:
			// Chain each subsequent pane below the previous one.
			id = splitTag(socket, prev, dir, app, "-v")
		}
		if id == "" {
			return
		}
		ids[i] = id
		prev = id
		if app == "scratch" || app == "yazi" {
			time.Sleep(sidebarSettle) // read the startup probe while focused
		}
	}
	applySizes(socket, panes, ids, layout.Sizes)
}
