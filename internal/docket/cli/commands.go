package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/docket/app"
	"github.com/schuettc/tackle/internal/docket/config"
	"github.com/schuettc/tackle/internal/docket/engine"
	"github.com/schuettc/tackle/internal/docket/gitx"
	"github.com/schuettc/tackle/internal/docket/hooks"
	"github.com/schuettc/tackle/internal/docket/item"
	"github.com/schuettc/tackle/internal/docket/observe"
	"github.com/schuettc/tackle/internal/docket/record"
	tools "github.com/schuettc/tools-common"
)

func flags(name, usage, long string, def func(fs *flag.FlagSet)) func() *flag.FlagSet {
	return func() *flag.FlagSet {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		tools.SetUsage(fs, usage, long)
		if def != nil {
			def(fs)
		}
		return fs
	}
}

func commands(stdin io.Reader) []tools.Command {
	ctx := context.Background()
	return []tools.Command{
		{
			Name: "init", Group: "setup", Synopsis: "init [<docket-repo-remote>] [--no-create] [--machine M] [--user U] [--root DIR]...",
			Summary:  "set up this machine: config plus a clone of the docket repo",
			NewFlags: initFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := initFlags()
				pos, err := parse(fs, args, out)
				if err != nil {
					return err
				}
				o := app.InitOptions{
					Machine:  fs.Lookup("machine").Value.String(),
					User:     fs.Lookup("user").Value.String(),
					Roots:    *fs.Lookup("root").Value.(*multi),
					NoCreate: boolFlag(fs, "no-create"),
				}
				if len(pos) > 0 {
					o.Remote = pos[0]
				}
				res, err := app.Init(ctx, o, newRunner())
				if err != nil {
					return err
				}
				if res.Created != "" {
					fmt.Fprintf(out, "created private GitHub repo %s\n", res.Created)
				}
				fmt.Fprintf(out, "docket initialized: machine %s, user %s, repo %s\nnext: docket hooks install && docket sync\n", res.Config.Machine, res.Config.User, res.Config.DocketRepo)
				return nil
			},
		},
		{
			Name: "sync", Group: "observe", Summary: "record, observe, rebuild the views, commit and push",
			NewFlags: syncFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := syncFlags()
				if _, err := parse(fs, args, out); err != nil {
					return err
				}
				a, err := open()
				if err != nil {
					return err
				}
				rep, err := a.Sync(ctx, app.SyncOptions{NoGitHub: boolFlag(fs, "no-github"), NoPush: boolFlag(fs, "no-push")})
				if errors.Is(err, app.ErrSyncBusy) {
					fmt.Fprintln(out, "another docket sync is running; skipped")
					return nil
				}
				if err != nil {
					return err
				}
				if boolFlag(fs, "json") {
					return tools.PrintJSON(out, rep)
				}
				printSync(out, a.Cfg.Machine, rep)
				return nil
			},
		},
		{
			Name: "attention", Group: "observe", Summary: "what needs you: new, due, drift, conflict and policy flags",
			NewFlags: filterFlags("attention"),
			Run: func(args []string, out, errw io.Writer) error {
				fs := filterFlags("attention")()
				if _, err := parse(fs, args, out); err != nil {
					return err
				}
				a, err := open()
				if err != nil {
					return err
				}
				res, _, err := a.Build(ctx)
				if err != nil {
					return err
				}
				items := filter(res.Attention(), fs)
				if boolFlag(fs, "json") {
					return tools.PrintJSON(out, map[string]any{"items": nonNil(items), "notices": res.Notices})
				}
				printItems(out, items, res.Notices)
				return nil
			},
		},
		{
			Name: "show", Group: "observe", Synopsis: "show <key>", Summary: "one item: decision, observation, flags and recent history",
			NewFlags: jsonFlags("show"),
			Run: func(args []string, out, errw io.Writer) error {
				fs := jsonFlags("show")()
				pos, err := parse(fs, args, out)
				if err != nil {
					return err
				}
				a, k, err := openKey(pos)
				if err != nil {
					return err
				}
				res, _, err := a.Build(ctx)
				if err != nil {
					return err
				}
				it, ok := res.Find(k.String())
				if !ok {
					return tools.Exitf(1, "%s is not a known item", k).WithHint("docket attention")
				}
				evs, _ := a.Events()
				hist := engine.History(evs, k)
				if len(hist) > 10 {
					hist = hist[len(hist)-10:]
				}
				if boolFlag(fs, "json") {
					return tools.PrintJSON(out, map[string]any{"item": it, "history": hist})
				}
				printItem(out, it, hist)
				return nil
			},
		},
		{
			Name: "history", Group: "observe", Synopsis: "history <key>", Summary: "every recorded action and decision for an item",
			NewFlags: jsonFlags("history"),
			Run: func(args []string, out, errw io.Writer) error {
				fs := jsonFlags("history")()
				pos, err := parse(fs, args, out)
				if err != nil {
					return err
				}
				a, k, err := openKey(pos)
				if err != nil {
					return err
				}
				evs, err := a.Events()
				if err != nil {
					return err
				}
				log, _ := a.Repo.Log(ctx, k.File())
				hist := engine.History(evs, k)
				if boolFlag(fs, "json") {
					return tools.PrintJSON(out, map[string]any{"decisions": log, "events": hist})
				}
				for i := len(log) - 1; i >= 0; i-- {
					fmt.Fprintf(out, "%s  decision  %s\n", log[i].Time.UTC().Format(time.RFC3339), log[i].Subject)
				}
				for _, ev := range hist {
					fmt.Fprintf(out, "%s  %s\n", ev.TS.UTC().Format(time.RFC3339), eventLine(ev))
				}
				return nil
			},
		},
		{
			Name: "decide", Group: "decide", Synopsis: "decide <key> <disposition> [--until C] [--note N]  |  decide --from <file>",
			Summary:  "record a decision (one commit, pushed at once)",
			Help:     "Dispositions: keep archive close delete merge wait watch ignore (wait/watch need --until).\nuntil: date(YYYY-MM-DD) merged(<pr>) closed(<pr|issue>) inactive(90d) released(<repo>)",
			NewFlags: decideFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := decideFlags()
				pos, err := parse(fs, args, out)
				if err != nil {
					return err
				}
				a, err := open()
				if err != nil {
					return err
				}
				o := app.DecideOptions{Until: strFlag(fs, "until"), Note: strFlag(fs, "note"), By: strFlag(fs, "by"), NoPush: boolFlag(fs, "no-push")}
				if from := strFlag(fs, "from"); from != "" {
					var b []byte
					if from == "-" {
						b, err = io.ReadAll(stdin)
					} else {
						b, err = os.ReadFile(from)
					}
					if err != nil {
						return err
					}
					entries, err := app.ParseTriage(b)
					if err != nil {
						return tools.UsageError{Msg: fmt.Sprintf("%s: %v", from, err)}
					}
					n, errs, pushed := a.DecideBatch(ctx, entries, o)
					fmt.Fprintf(out, "recorded %d decision(s)%s\n", n, pushedText(pushed, n > 0 && !o.NoPush))
					for _, e := range errs {
						fmt.Fprintf(errw, "  %v\n", e)
					}
					if len(errs) > 0 {
						return tools.Exitf(1, "%d entr(ies) not recorded", len(errs))
					}
					return nil
				}
				if len(pos) != 2 {
					return tools.UsageError{Msg: "decide needs <key> <disposition> (or --from <file>)"}
				}
				d, pushed, err := a.Decide(ctx, pos[0], pos[1], o)
				if err != nil {
					return tools.Exitf(1, "%v", err).WithHint("docket decide --help")
				}
				k, _ := item.ParseKey(pos[0])
				fmt.Fprintf(out, "decided %s → %s by %s%s\n", k, d.Disposition, d.DecidedBy, pushedText(pushed, !o.NoPush))
				return nil
			},
		},
		{
			Name: "triage", Group: "decide", Summary: "write a worksheet of items needing a decision (apply with decide --from)",
			NewFlags: triageFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := triageFlags()
				if _, err := parse(fs, args, out); err != nil {
					return err
				}
				a, err := open()
				if err != nil {
					return err
				}
				res, _, err := a.Build(ctx)
				if err != nil {
					return err
				}
				items := filter(res.Attention(), fs)
				sheet := app.TriageFile(items, a.Now())
				dest := strFlag(fs, "out")
				if dest == "-" {
					_, err := out.Write(sheet)
					return err
				}
				if err := os.WriteFile(dest, sheet, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(out, "wrote %d item(s) to %s; fill it in, then: docket decide --from %s\n", len(items), dest, dest)
				return nil
			},
		},
		{
			Name: "validate", Group: "decide", Summary: "check every decision file and policy.toml",
			Run: func(args []string, out, errw io.Writer) error {
				a, err := open()
				if err != nil {
					return err
				}
				errs := a.Repo.Validate()
				for _, e := range errs {
					fmt.Fprintf(errw, "  %v\n", e)
				}
				if len(errs) > 0 {
					return tools.Exitf(1, "%d problem(s)", len(errs))
				}
				ds, _ := a.Repo.Decisions()
				fmt.Fprintf(out, "ok: %d decision(s), policy valid\n", len(ds))
				return nil
			},
		},
		{
			Name: "doctor", Group: "observe", Summary: "check config, repo, gh, hooks and owner reachability",
			NewFlags: jsonFlags("doctor"),
			Run: func(args []string, out, errw io.Writer) error {
				fs := jsonFlags("doctor")()
				if _, err := parse(fs, args, out); err != nil {
					return err
				}
				a, err := open()
				if err != nil {
					return err
				}
				checks := a.Doctor(ctx, hookOpts(), executable())
				if boolFlag(fs, "json") {
					if err := tools.PrintJSON(out, checks); err != nil {
						return err
					}
				} else {
					for _, c := range checks {
						fmt.Fprintf(out, "[%-4s] %s: %s\n", c.Status, c.Name, c.Detail)
					}
				}
				for _, c := range checks {
					if c.Status == "fail" {
						return tools.Exitf(1, "doctor found failures")
					}
				}
				return nil
			},
		},
		{
			Name: "hooks", Group: "setup", Synopsis: "hooks install|uninstall|status [--json]",
			Summary:  "manage the global git hook shims that journal git activity",
			NewFlags: jsonFlags("hooks"),
			Run: func(args []string, out, errw io.Writer) error {
				fs := jsonFlags("hooks")()
				pos, err := parse(fs, args, out)
				if err != nil {
					return err
				}
				if len(pos) != 1 {
					return tools.UsageError{Msg: "hooks needs install, uninstall or status"}
				}
				o := hookOpts()
				switch pos[0] {
				case "install":
					if err := hooks.Install(ctx, o); err != nil {
						return err
					}
					st, _ := hooks.GetStatus(ctx, o)
					fmt.Fprintf(out, "global core.hooksPath → %s (chains to %s)\n", o.Dir, orText(st.Prev, "each repo's own hooks"))
				case "uninstall":
					if err := hooks.Uninstall(ctx, o); err != nil {
						return err
					}
					fmt.Fprintln(out, "hooks removed; global core.hooksPath restored")
				case "status":
					st, err := hooks.GetStatus(ctx, o)
					if err != nil {
						return err
					}
					if boolFlag(fs, "json") {
						return tools.PrintJSON(out, map[string]any{"installed": st.Installed, "global_hooks_path": st.GlobalHooksPath,
							"prev": st.Prev, "binary": st.Binary, "binary_ok": st.BinaryOK, "missing": nonNil(st.Missing)})
					}
					fmt.Fprintf(out, "installed: %v\nglobal core.hooksPath: %s\nchains to: %s\nbinary: %s (ok: %v)\n", st.Installed,
						orText(st.GlobalHooksPath, "(unset)"), orText(st.Prev, "each repo's own hooks"), st.Binary, st.BinaryOK)
				default:
					return tools.UsageError{Msg: "hooks needs install, uninstall or status"}
				}
				return nil
			},
		},
		{
			Name: "brief", Group: "plumbing", Summary: "session-start briefing for the repo at --cwd (silent when nothing to say)",
			NewFlags: briefFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := briefFlags()
				if _, err := parse(fs, args, io.Discard); err != nil {
					return nil
				}
				a, err := app.Open(newRunner())
				if err != nil {
					return nil
				}
				cwd := strFlag(fs, "cwd")
				if cwd == "" {
					cwd, _ = os.Getwd()
				}
				res, snaps, err := a.Build(ctx)
				if err != nil {
					return nil
				}
				repo := ""
				if url, err := gitx.Run(ctx, cwd, "remote", "get-url", "origin"); err == nil {
					repo = observe.GitHubRepo(url)
				}
				if repo == "" {
					repo = engine.RepoForPath(snaps, a.Cfg.Machine, cwd)
				}
				if repo == "" {
					return nil
				}
				max := 8
				fmt.Sscanf(strFlag(fs, "max"), "%d", &max)
				fmt.Fprint(out, engine.Brief(res, repo, max))
				return nil
			},
		},
		{
			Name: "record", Group: "plumbing", Summary: "journal a harness's shell command (JSON on stdin; never fails)",
			NewFlags: recordFlags,
			Run: func(args []string, out, errw io.Writer) error {
				fs := recordFlags()
				if _, err := parse(fs, args, io.Discard); err != nil {
					return nil
				}
				if _, err := config.Load(); err != nil {
					return nil
				}
				record.Main(strFlag(fs, "harness"), stdin, config.SpoolDir(), time.Now())
				return nil
			},
		},
		{Name: "hook", Group: "plumbing", Synopsis: "hook <git-hook-name> [args]", Summary: "called by the git hook shims (never fails)"},
	}
}

func openKey(pos []string) (*app.App, item.Key, error) {
	if len(pos) != 1 {
		return nil, item.Key{}, tools.UsageError{Msg: "needs exactly one item key, e.g. repo:owner/name"}
	}
	k, err := item.ParseKey(pos[0])
	if err != nil {
		return nil, k, tools.UsageError{Msg: err.Error()}
	}
	a, err := open()
	return a, k, err
}

func initFlags() *flag.FlagSet {
	return flags("init", "docket init [remote] [flags]", "Idempotent. With no remote, uses <your GitHub login>/docket-data, creating it as a PRIVATE repo if it doesn't exist (refuses a public one). The first machine bootstraps an empty repo.", func(fs *flag.FlagSet) {
		fs.Bool("no-create", false, "fail instead of creating a missing GitHub repo")
		fs.String("machine", "", "machine name (default: short host name)")
		fs.String("user", "", "your GitHub login (default: gh's)")
		fs.Var(new(multi), "root", "directory to scan for clones (repeatable; default ~/GitHub and ~/dotfiles)")
	})()
}

func syncFlags() *flag.FlagSet {
	return flags("sync", "docket sync [flags]", "Runs from launchd every 30 minutes once layer 1b is installed; safe to run any time.", func(fs *flag.FlagSet) {
		fs.Bool("no-github", false, "skip the GitHub refresh (use the cache)")
		fs.Bool("no-push", false, "commit locally only")
		fs.Bool("json", false, "print the report as JSON")
	})()
}

func filterFlags(name string) func() *flag.FlagSet {
	return flags(name, "docket "+name+" [flags]", "", func(fs *flag.FlagSet) {
		fs.String("kind", "", "only this kind: repo, pr, issue, branch, worktree")
		fs.String("repo", "", "only items of this owner/name")
		fs.Bool("json", false, "print JSON")
	})
}

func jsonFlags(name string) func() *flag.FlagSet {
	return flags(name, "", "", func(fs *flag.FlagSet) { fs.Bool("json", false, "print JSON") })
}

func decideFlags() *flag.FlagSet {
	return flags("decide", "docket decide <key> <disposition> [flags] | docket decide --from <file>", "", func(fs *flag.FlagSet) {
		fs.String("until", "", "condition: date(...), merged(...), closed(...), inactive(...), released(...)")
		fs.String("note", "", "why")
		fs.String("by", "", "who decided (default: this session or your GitHub login)")
		fs.String("from", "", "apply a triage worksheet ('-' = stdin)")
		fs.Bool("no-push", false, "commit locally only")
	})()
}

func triageFlags() *flag.FlagSet {
	return flags("triage", "docket triage [flags]", "", func(fs *flag.FlagSet) {
		fs.String("kind", "", "only this kind")
		fs.String("repo", "", "only items of this owner/name")
		fs.String("out", "docket-triage.toml", "worksheet path ('-' = stdout)")
	})()
}

func briefFlags() *flag.FlagSet {
	return flags("brief", "docket brief [--cwd DIR] [--max N]", "", func(fs *flag.FlagSet) {
		fs.String("cwd", "", "directory of the session (default: current)")
		fs.String("max", "8", "most items to list")
	})()
}

func recordFlags() *flag.FlagSet {
	return flags("record", "docket record --harness claude|pi < payload.json", "", func(fs *flag.FlagSet) {
		fs.String("harness", "claude", "payload format: claude (PostToolUse) or pi (pi-docket)")
	})()
}

func boolFlag(fs *flag.FlagSet, name string) bool  { return fs.Lookup(name).Value.String() == "true" }
func strFlag(fs *flag.FlagSet, name string) string { return fs.Lookup(name).Value.String() }

func filter(items []engine.Item, fs *flag.FlagSet) []engine.Item {
	kind, repo := strFlag(fs, "kind"), strings.ToLower(strFlag(fs, "repo"))
	var out []engine.Item
	for _, it := range items {
		if (kind == "" || string(it.Kind) == kind) && (repo == "" || it.Repo == repo) {
			out = append(out, it)
		}
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func orText(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func pushedText(pushed, tried bool) string {
	switch {
	case pushed:
		return "; pushed"
	case tried:
		return "; remote unreachable, queued for the next sync"
	}
	return "; not pushed (--no-push)"
}
