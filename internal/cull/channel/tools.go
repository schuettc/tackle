package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/schuettc/tackle/internal/cull/apply"
	"github.com/schuettc/tackle/internal/cull/check"
	"github.com/schuettc/tackle/internal/cull/discover"
	"github.com/schuettc/tackle/internal/cull/jev"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/key"
	"github.com/schuettc/tackle/internal/cull/timing"
	"github.com/schuettc/tools-common/channelmcp"
)

func schema(s string) json.RawMessage { return json.RawMessage(s) }

const pathProp = `"path":{"type":"string","description":"a path inside the project to use instead of the session's project root"}`

// Tools is cull's tool list. None of them sets an answer or presses Send:
// those are Court's, on the page.
func Tools() []channelmcp.Tool {
	return []channelmcp.Tool{
		{Name: "cull_check", Description: "Judge the project's automated tests with Jev and record what Court must review. base: the merge base of your branch to judge only the tests you changed (omit for the whole suite). Returns the summary, the ids of the tests to cut, the ids of the groups to merge, to_review (how many items wait for Court on the review page), and speed (a digest of what makes the suite slow or weak: fixed waits, setup that blocks parallel tests, swallowed waits, screenshots).",
			InputSchema: schema(`{"type":"object","properties":{"base":{"type":"string","description":"git ref; judge only tests changed since it"},` + pathProp + `}}`)},
		{Name: "cull_apply", Description: "Remove tests from the last cull_check. Without ids it removes every cut (Jev's and Court's), tidies the imports that leaves unused, runs the project's tests before and after, and restores everything if they fail. Never commits. Returns what was applied, what needs you, the tidy report, the verify outcome and the exit (applied, rolled back, rollback failed or refused) with the reason.",
			InputSchema: schema(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string"},"description":"remove exactly these test ids instead of every cut"},` + pathProp + `}}`)},
		{Name: "cull_check_group", Description: "Verify your rewrite of a near-duplicate group (an id from cull_check's merge list) as one table test: the originals are gone, one new test keeps every row, Jev does not flag it, and the tests pass. Returns the four checks and their results.",
			InputSchema: schema(`{"type":"object","properties":{"id":{"type":"string","description":"the group id"},` + pathProp + `},"required":["id"]}`)},
		{Name: "cull_time", Description: "Run the checks the project runs (CI, hooks, recipes) one at a time with timing and return where the time goes: wall time per check, the slowest Go packages and Python files with their share, the 20 slowest tests, and hints (Go packages whose tests are slow and blocked from running in parallel by a t.Setenv helper; Python files that spend half their time in setup). Before each check it first runs the commands that set that check up in the project's recipe or CI step (for example a build), once per check, and reports their time as setup; a failing setup command fails that check. Takes minutes on a large suite. Run it before and after fixing what it and cull_check's speed findings point at.",
			InputSchema: schema(`{"type":"object","properties":{` + pathProp + `}}`)},
		{Name: "cull_review", Description: "Open cull's review page for this project (or the repository at path) in Court's browser and make this session the one his answers are sent to. Returns the page URL and how many items are open. Says so, and opens nothing, when there is nothing to review. Use once; never repeatedly.",
			InputSchema: schema(`{"type":"object","properties":{` + pathProp + `}}`)},
		{Name: "cull_status", Description: "What is open, answered and sent for this project (or the repository at path), who owns the review, and any sends not yet delivered.",
			InputSchema: schema(`{"type":"object","properties":{` + pathProp + `}}`)},
	}
}

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// oneLine makes an error message one line.
func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", "; ")), " ")
}

// Call runs one tool. Nothing it returns can contain the TypeSafe key.
func (ch *Channel) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	text, err := ch.call(ctx, name, args)
	k, _, kerr := key.Load()
	if kerr != nil || k == "" {
		k = ""
	}
	scrub := func(s string) string {
		if k != "" {
			s = strings.ReplaceAll(s, k, "[key]")
		}
		return s
	}
	if err != nil {
		return "", errors.New(scrub(oneLine(err.Error())))
	}
	return scrub(text), nil
}

func (ch *Channel) call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var a struct {
		Path string   `json:"path"`
		Base string   `json:"base"`
		IDs  []string `json:"ids"`
		ID   string   `json:"id"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	switch name {
	case "cull_check":
		return ch.check(ctx, a.Path, a.Base)
	case "cull_apply":
		return ch.apply(ctx, a.Path, a.IDs)
	case "cull_check_group":
		if a.ID == "" {
			return "", errors.New("id is required")
		}
		return ch.checkGroup(ctx, a.Path, a.ID)
	case "cull_time":
		return ch.timeRun(ctx, a.Path)
	case "cull_review":
		return ch.review(ctx, a.Path)
	case "cull_status":
		return ch.status(ctx, a.Path)
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// where resolves the path argument: the session's project root by default, a
// relative path against the session's directory.
func (ch *Channel) where(path string) (string, error) {
	switch {
	case path == "":
		if ch.rootErr != nil {
			return "", ch.rootErr
		}
		return ch.root, nil
	case filepath.IsAbs(path):
		return path, nil
	}
	return filepath.Join(ch.ID.CWD, path), nil
}

// judging prepares what check and check_group need: the project's root, a
// Jev client, and the refusals a missing key or egress setting deserve.
func (ch *Channel) judging(path string) (string, string, judge.Evaluator, error) {
	p, err := ch.where(path)
	if err != nil {
		return "", "", nil, err
	}
	root, err := discover.Root(p)
	if err != nil {
		return "", "", nil, err
	}
	cfg, _, err := check.LoadConfig(root)
	if err != nil {
		return "", "", nil, err
	}
	if !cfg.Egress {
		return "", "", nil, fmt.Errorf("egress is off for %s: Court needs to run cull init", root)
	}
	k, _, err := key.Load()
	if errors.Is(err, key.ErrMissing) {
		return "", "", nil, errors.New("no TypeSafe key: Court needs to run cull init")
	}
	if err != nil {
		return "", "", nil, err
	}
	return p, root, jev.NewClient(k), nil
}

func judgeErr(err error) error {
	if errors.Is(err, jev.ErrUnauthorized) {
		return errors.New("TypeSafe rejected the key: Court needs to run cull init")
	}
	return err
}

func (ch *Channel) check(ctx context.Context, path, base string) (string, error) {
	p, root, ev, err := ch.judging(path)
	if err != nil {
		return "", err
	}
	rep, err := check.Run(ctx, ev, check.Options{Path: p, Diff: base, Stdout: io.Discard, Stderr: ch.Log})
	if err != nil {
		return "", judgeErr(err)
	}
	cut, merge := []string{}, []string{}
	for _, t := range rep.Tests {
		if t.Err == "" && t.Verdict == "cut" {
			cut = append(cut, t.ID)
		}
	}
	for _, g := range rep.Groups {
		if g.Err == "" && g.Verdict == "consolidate" {
			merge = append(merge, g.ID)
		}
	}
	return pretty(map[string]any{
		"root": root, "mode": rep.Mode, "base": rep.Base, "summary": rep.Summary,
		"cut": cut, "merge": merge,
		// Items on the review page that Court has not answered.
		"to_review": rep.Summary["review"] + rep.Summary["group_review"],
		// What makes the suite slow or weak: counts per kind, the literal wait
		// total in seconds, and the 20 costliest findings. Fix these.
		"speed": rep.SpeedDigest(),
	}), nil
}

func (ch *Channel) checkGroup(ctx context.Context, path, id string) (string, error) {
	_, root, ev, err := ch.judging(path)
	if err != nil {
		return "", err
	}
	gc, err := check.CheckGroup(ctx, ev, root, id, check.Options{Stdout: io.Discard, Stderr: ch.Log})
	if err != nil {
		return "", judgeErr(err)
	}
	return pretty(gc), nil
}

type verifyLine struct {
	Command    string `json:"command"`
	OK         bool   `json:"ok"`
	TimedOut   bool   `json:"timed_out,omitempty"`
	ExitCode   int    `json:"exit_code,omitempty"`
	OutputTail string `json:"output_tail,omitempty"`
}

// apply is exactly `cull apply`: the same apply.Run (preflight, baseline,
// rollback), its exit code mapped to words for the agent.
func (ch *Channel) apply(ctx context.Context, path string, ids []string) (string, error) {
	p, err := ch.where(path)
	if err != nil {
		return "", err
	}
	root, err := discover.Root(p)
	if err != nil {
		return "", err
	}
	cfg, _, err := check.LoadConfig(root)
	if err != nil {
		return "", err
	}
	out, runErr := apply.Run(ctx, apply.Options{
		Root: root, IDs: ids, VerdictCut: len(ids) == 0, Timeout: apply.DefaultTimeout,
		TestCommand: cfg.TestCommand, Stderr: io.Discard, Notice: io.Discard,
	})

	exit, reason := "applied", ""
	if runErr != nil {
		exit, reason = "refused", runErr.Error()
		var ee *apply.ExitError
		if (errors.As(runErr, &ee) && ee.Code == 1) || out.RolledBack {
			exit = "rolled back"
		}
		if out.RollbackFailed {
			exit = "rollback failed"
		}
	}
	var results []verifyLine
	verifyState := "skipped"
	if len(out.After) > 0 {
		verifyState = "ok"
	}
	for _, r := range out.After {
		l := verifyLine{Command: r.Command, OK: r.OK, TimedOut: r.TimedOut, ExitCode: r.ExitCode}
		if !r.OK {
			verifyState = "failed"
			l.OutputTail = r.OutputTail
		}
		results = append(results, l)
	}
	res := map[string]any{
		"root": root, "exit": exit, "applied": nilToEmpty(out.Applied), "needs_agent": out.NeedsAgent, "refused": out.Refused,
		"files": nilToEmpty(out.Files), "imports_removed": out.ImportsRemoved, "orphaned_helpers": out.OrphanedHelpers,
		"verify": verifyState, "verify_results": results,
		"rolled_back": out.RolledBack, "rollback_failed": out.RollbackFailed,
	}
	if reason != "" {
		res["reason"] = reason
	}
	if out.Snapshot != "" {
		res["snapshot"] = out.Snapshot
	}
	return pretty(res), nil
}

func nilToEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type agentStatus struct {
	Project  int64  `json:"project"`
	Open     int    `json:"open"`
	Answered int    `json:"answered"`
	Sent     int    `json:"sent"`
	Owner    string `json:"owner"`
}

func (ch *Channel) status(ctx context.Context, path string) (string, error) {
	root, err := ch.where(path)
	if err != nil {
		return "", err
	}
	var out map[string]any
	if _, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/status?"+q("root", root), nil, &out); err != nil {
		return "", err
	}
	return pretty(out), nil
}

func (ch *Channel) review(ctx context.Context, path string) (string, error) {
	root, err := ch.where(path)
	if err != nil {
		return "", err
	}
	if ch.ID.Session == "" {
		return "", errors.New("this cull channel has no session id (not running under Claude Code or pi), so Court's answers could not be sent to it")
	}
	var st agentStatus
	if _, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/status?"+q("root", root), nil, &st); err != nil {
		return "", err
	}
	if st.Open == 0 {
		return pretty(map[string]any{"open": 0, "message": "nothing to review"}), nil
	}
	var rv struct {
		Project int64  `json:"project"`
		URL     string `json:"url"`
	}
	if _, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/review", map[string]any{"session": ch.ID.Session, "root": root}, &rv); err != nil {
		return "", err
	}
	res := map[string]any{"url": rv.URL, "open": st.Open, "opened": true}
	if err := ch.Open(rv.URL); err != nil {
		res["opened"] = false
		res["open_error"] = oneLine(err.Error())
	}
	return pretty(res), nil
}

func (ch *Channel) timeRun(ctx context.Context, path string) (string, error) {
	p, err := ch.where(path)
	if err != nil {
		return "", err
	}
	root, err := discover.Root(p)
	if err != nil {
		return "", err
	}
	cfg, _, err := check.LoadConfig(root)
	if err != nil {
		return "", err
	}
	rep, err := timing.Run(ctx, timing.Options{Root: root, TestCommand: cfg.TestCommand})
	if err != nil {
		return "", err
	}
	return pretty(rep), nil
}
