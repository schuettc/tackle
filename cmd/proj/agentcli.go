package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/proj"
)

// parseNewTarget splits "<project>/<work>" on the FIRST '/', slugging and
// validating the work segment. It errors on a missing '/' or an invalid work
// name (after slugging).
func parseNewTarget(s string) (project, work string, err error) {
	i := strings.Index(s, "/")
	if i < 0 {
		return "", "", fmt.Errorf("target %q must be <project>/<work>", s)
	}
	project = s[:i]
	work = proj.SlugWork(s[i+1:])
	if project == "" {
		return "", "", fmt.Errorf("target %q must be <project>/<work>", s)
	}
	if !proj.ValidWork(work) {
		return "", "", fmt.Errorf("invalid work name %q", work)
	}
	return project, work, nil
}

// sessionJSON is the per-session shape emitted by `proj list --json`.
type sessionJSON struct {
	Name           string `json:"name"`
	Project        string `json:"project"`
	Socket         string `json:"socket"`
	Agent          string `json:"agent"`
	State          string `json:"state"`
	Dir            string `json:"dir"`
	Unread         int    `json:"unread"`
	ActionRequired int    `json:"action_required"`
}

// listJSON is the top-level shape emitted by `proj list --json`.
type listJSON struct {
	Projects []string      `json:"projects"`
	Sessions []sessionJSON `json:"sessions"`
}

// cmdList implements `proj list --json`: the configured project names plus every
// live session across the per-project servers.
func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*asJSON {
		fmt.Fprintln(os.Stderr, "proj list: only --json is supported")
		return 2
	}

	out := listJSON{Projects: []string{}, Sessions: []sessionJSON{}}
	if roots, err := proj.LoadRoots(); err == nil {
		for _, dir := range roots.AllProjectDirs() {
			out.Projects = append(out.Projects, filepath.Base(dir))
		}
	}
	for _, s := range proj.LiveSessions() {
		out.Sessions = append(out.Sessions, sessionJSON{
			Name:           s.Name,
			Project:        proj.ProjectFromSocket(s.Socket),
			Socket:         s.Socket,
			Agent:          s.Agent,
			State:          s.State,
			Dir:            s.Dir,
			Unread:         s.Unread,
			ActionRequired: s.ActionRequired,
		})
	}
	return emitJSON(out)
}

// currentJSON is the shape emitted by `proj current --json`.
type currentJSON struct {
	Project string `json:"project"`
	Work    string `json:"work"`
	Alias   string `json:"alias"`
	Dir     string `json:"dir"`
}

// cmdCurrent implements `proj current --json`: the identity of the ambient proj
// session derived from $TMUX. All fields are empty when outside a proj session.
func cmdCurrent(args []string) int {
	fs := flag.NewFlagSet("current", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*asJSON {
		fmt.Fprintln(os.Stderr, "proj current: only --json is supported")
		return 2
	}

	var cur currentJSON
	socketPath := proj.SocketFromEnv()
	project := proj.ProjectFromSocket(socketPath)
	if project != "" {
		socket := proj.CurrentServer() // socket base name for tmux -L
		name, err := proj.Run(socket, "display-message", "-p", "#{session_name}")
		if err == nil && name != "" {
			label := proj.Query(socket, name, "#{"+proj.LabelOption()+"}")
			cur.Project = project
			cur.Work = label
			if label != "" {
				cur.Alias = proj.AliasFor(socketPath, label)
			}
			cur.Dir = proj.Query(socket, name, "#{pane_current_path}")
		}
	}
	return emitJSON(cur)
}

// cmdNew implements `proj new <project>/<work> [--agent X] [--no-sidebar]`.
//
// Detached by contract — an agent mints a workspace but never switches the
// operator's client. This handler therefore calls EnsureSession only and NEVER
// calls Goto (spec boundary). --no-sidebar suppresses sidebar spawn; otherwise
// sidebar is spawned when Config.SidebarFor(project) is true.
func cmdNew(args []string) int {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	agent := fs.String("agent", "", "agent to launch (defaults to config)")
	noSidebar := fs.Bool("no-sidebar", false, "suppress the sidebar (Phase 3)")

	// Allow flags interspersed with the positional target.
	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "usage: proj new <project>/<work> [--agent X] [--no-sidebar]")
		return 2
	}

	project, work, err := parseNewTarget(positional[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "proj: %v\n", err)
		return 2
	}

	roots, err := proj.LoadRoots()
	if err != nil {
		fmt.Fprintf(os.Stderr, "proj: %v\n", err)
		return 1
	}
	dir, ok := roots.DirForName(project)
	if !ok {
		fmt.Fprintf(os.Stderr, "proj: unknown project %q\n", project)
		return 1
	}

	cfg := proj.LoadConfig()
	a := *agent
	if a == "" {
		a = cfg.AgentFor(project)
	}

	name := proj.SessionName(project, work)
	socket := proj.SocketFor(project)
	if err := proj.EnsureSession(socket, name, dir, a); err != nil {
		fmt.Fprintf(os.Stderr, "proj: %v\n", err)
		return 1
	}
	if !*noSidebar && cfg.SidebarFor(project) {
		SpawnSidebarDetached(socket, name, dir)
	}
	fmt.Println(name)
	return 0
}

// sidebarArgs holds the parsed result of parseSidebarArgs.
type sidebarArgs struct {
	session string
	socket  string
	dir     string
}

// parseSidebarArgs parses `sidebar <session> [--socket S] [--dir D]`.
func parseSidebarArgs(args []string) (sidebarArgs, error) {
	fs := flag.NewFlagSet("sidebar", flag.ContinueOnError)
	socket := fs.String("socket", "", "tmux socket name")
	dir := fs.String("dir", "", "working directory for sidebar panes")

	var positional []string
	rest := args
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return sidebarArgs{}, err
		}
		rest = fs.Args()
		if len(rest) > 0 {
			positional = append(positional, rest[0])
			rest = rest[1:]
		}
	}

	if len(positional) != 1 {
		return sidebarArgs{}, fmt.Errorf("usage: proj sidebar <session> [--socket S] [--dir D]")
	}
	return sidebarArgs{session: positional[0], socket: *socket, dir: *dir}, nil
}

// cmdSidebar implements `proj sidebar <session> [--socket S] [--dir D]`.
// Socket resolution order: --socket → CurrentServer() (if non-empty) →
// FindServer(session). If none resolve, exits 1. Dir defaults to the session's
// #{pane_current_path} when --dir is absent. Layout comes from LoadConfig().
func cmdSidebar(args []string) int {
	sa, err := parseSidebarArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proj:", err)
		return 2
	}

	socket := sa.socket
	if socket == "" {
		socket = proj.CurrentServer()
	}
	if socket == "" {
		if s, ok := proj.FindServer(sa.session); ok {
			socket = s
		}
	}
	if socket == "" {
		fmt.Fprintf(os.Stderr, "proj: cannot resolve socket for session %q\n", sa.session)
		return 1
	}

	dir := sa.dir
	if dir == "" {
		dir = proj.Query(socket, sa.session, "#{pane_current_path}")
	}

	layout := proj.LoadConfig().SidebarLayout
	proj.BuildSidebar(socket, sa.session, dir, layout)
	return 0
}

// emitJSON writes v as indented JSON to stdout, returning the process exit code.
func emitJSON(v any) int {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "proj: %v\n", err)
		return 1
	}
	fmt.Println(string(b))
	return 0
}
