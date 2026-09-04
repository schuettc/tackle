package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/proj"
	tools "github.com/schuettc/tools-common"
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

// listFlags holds `proj list`'s parsed flags.
type listFlags struct {
	json *bool
}

// newListFlags is proj list's side-effect-free flag constructor, used both to
// parse and to render -h/help.
func newListFlags() (*flag.FlagSet, *listFlags) {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	f := &listFlags{json: fs.Bool("json", false, "emit JSON")}
	tools.SetUsage(fs, "proj list --json",
		"Lists configured project names and every live session across proj's per-project tmux servers.")
	return fs, f
}

// cmdList implements `proj list --json`: the configured project names plus every
// live session across the per-project servers.
func cmdList(args []string, out, errw io.Writer) error {
	fs, f := newListFlags()
	if err := tools.ParseFlags(fs, args, out); err != nil {
		return err
	}
	if !*f.json {
		return tools.UsageError{Msg: "only --json is supported"}
	}

	payload := listJSON{Projects: []string{}, Sessions: []sessionJSON{}}
	if roots, err := proj.LoadRoots(); err == nil {
		for _, dir := range roots.AllProjectDirs() {
			payload.Projects = append(payload.Projects, filepath.Base(dir))
		}
	}
	for _, s := range proj.LiveSessions() {
		payload.Sessions = append(payload.Sessions, sessionJSON{
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
	return writeJSON(out, payload)
}

// currentJSON is the shape emitted by `proj current --json`.
type currentJSON struct {
	Project string `json:"project"`
	Work    string `json:"work"`
	Alias   string `json:"alias"`
	Dir     string `json:"dir"`
}

// currentFlags holds `proj current`'s parsed flags.
type currentFlags struct {
	json *bool
}

// newCurrentFlags is proj current's side-effect-free flag constructor.
func newCurrentFlags() (*flag.FlagSet, *currentFlags) {
	fs := flag.NewFlagSet("current", flag.ContinueOnError)
	f := &currentFlags{json: fs.Bool("json", false, "emit JSON")}
	tools.SetUsage(fs, "proj current --json",
		"Identifies the ambient proj session from $TMUX. All fields are empty when run outside a proj session.")
	return fs, f
}

// cmdCurrent implements `proj current --json`: the identity of the ambient proj
// session derived from $TMUX. All fields are empty when outside a proj session.
func cmdCurrent(args []string, out, errw io.Writer) error {
	fs, f := newCurrentFlags()
	if err := tools.ParseFlags(fs, args, out); err != nil {
		return err
	}
	if !*f.json {
		return tools.UsageError{Msg: "only --json is supported"}
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
	return writeJSON(out, cur)
}

// newFlags holds `proj new`'s parsed flags.
type newFlags struct {
	agent     *string
	noSidebar *bool
}

// newNewFlags is proj new's side-effect-free flag constructor.
func newNewFlags() (*flag.FlagSet, *newFlags) {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	f := &newFlags{
		agent:     fs.String("agent", "", "agent to launch (defaults to config)"),
		noSidebar: fs.Bool("no-sidebar", false, "suppress the sidebar (Phase 3)"),
	}
	tools.SetUsage(fs, "new <project>/<work> [--agent X] [--no-sidebar]",
		"Creates or resumes the tmux session for <project>/<work>. Detached by contract: "+
			"mints the session but never switches the caller's client. --no-sidebar suppresses "+
			"the sidebar; otherwise it spawns when the project's config enables it.")
	return fs, f
}

// cmdNew implements `proj new <project>/<work> [--agent X] [--no-sidebar]`.
//
// Detached by contract — an agent mints a workspace but never switches the
// operator's client. This handler therefore calls EnsureSession only and NEVER
// calls Goto (spec boundary). --no-sidebar suppresses sidebar spawn; otherwise
// sidebar is spawned when Config.SidebarFor(project) is true.
func cmdNew(args []string, out, errw io.Writer) error {
	fs, f := newNewFlags()
	flagArgs, positional := tools.SplitArgs(fs, args)
	if err := tools.ParseFlags(fs, flagArgs, out); err != nil {
		return err
	}
	if len(positional) != 1 {
		return tools.UsageError{Msg: "usage: proj new <project>/<work> [--agent X] [--no-sidebar]"}
	}

	project, work, err := parseNewTarget(positional[0])
	if err != nil {
		return tools.Exitf(1, "%v", err)
	}

	roots, err := proj.LoadRoots()
	if err != nil {
		return tools.Exitf(1, "%v", err)
	}
	dir, ok := roots.DirForName(project)
	if !ok {
		return tools.Exitf(1, "unknown project %q", project)
	}

	cfg := proj.LoadConfig()
	a := *f.agent
	if a == "" {
		a = cfg.AgentFor(project)
	}

	name := proj.SessionName(project, work)
	socket := proj.SocketFor(project)
	if err := proj.EnsureSession(socket, name, dir, a); err != nil {
		return tools.Exitf(1, "%v", err)
	}
	if !*f.noSidebar && cfg.SidebarFor(project) {
		SpawnSidebarDetached(socket, name, dir)
	}
	fmt.Fprintln(out, name)
	return nil
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
		// ParseFlags surfaces -h as flag.ErrHelp (after printing the flags);
		// cmdSidebar turns that into a clean exit.
		if err := tools.ParseFlags(fs, rest, os.Stdout); err != nil {
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

// sidebarFlags holds `proj sidebar`'s flags, for -h/help rendering. Actual
// parsing goes through parseSidebarArgs, whose signature is pinned by tests.
type sidebarFlags struct {
	socket *string
	dir    *string
}

// newSidebarFlags is proj sidebar's side-effect-free flag constructor.
func newSidebarFlags() (*flag.FlagSet, *sidebarFlags) {
	fs := flag.NewFlagSet("sidebar", flag.ContinueOnError)
	f := &sidebarFlags{
		socket: fs.String("socket", "", "tmux socket name"),
		dir:    fs.String("dir", "", "working directory for sidebar panes"),
	}
	tools.SetUsage(fs, "sidebar <session> [--socket S] [--dir D]",
		"Opens the sidebar panes for an existing session. Socket resolves from --socket, "+
			"then the ambient server, then a server search by session name. --dir defaults to "+
			"the session's current pane directory.")
	return fs, f
}

// cmdSidebar implements `proj sidebar <session> [--socket S] [--dir D]`.
// Socket resolution order: --socket → CurrentServer() (if non-empty) →
// FindServer(session). If none resolve, exits 1. Dir defaults to the session's
// #{pane_current_path} when --dir is absent. Layout comes from LoadConfig().
func cmdSidebar(args []string, out, errw io.Writer) error {
	sa, err := parseSidebarArgs(args)
	if err != nil {
		return tools.UsageError{Msg: err.Error()}
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
		return tools.Exitf(1, "cannot resolve socket for session %q", sa.session)
	}

	dir := sa.dir
	if dir == "" {
		dir = proj.Query(socket, sa.session, "#{pane_current_path}")
	}

	layout := proj.LoadConfig().SidebarLayout
	proj.BuildSidebar(socket, sa.session, dir, layout)
	return nil
}

// writeJSON writes v as indented JSON to out, wrapping a marshal failure as a
// runtime (exit 1) error.
func writeJSON(out io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return tools.Exitf(1, "%v", err)
	}
	fmt.Fprintln(out, string(b))
	return nil
}
