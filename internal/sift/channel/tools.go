package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/schuettc/tackle/internal/sift/apply"
	"github.com/schuettc/tackle/internal/sift/audit"
	"github.com/schuettc/tackle/internal/sift/clean"
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/rec"
	"github.com/schuettc/tackle/internal/sift/recommend"
	"github.com/schuettc/tackle/internal/sift/store"
	"github.com/schuettc/tools-common/channelmcp"
)

func schema(s string) json.RawMessage { return json.RawMessage(s) }

const roundProp = `"round":{"type":"integer","description":"the round (default: the latest)"}`

const proposeSchema = `{"type":"object","properties":{` + roundProp + `,` +
	`"recommendations":{"type":"array","description":"one per file; linked files in the same call","items":{"type":"object","properties":{` +
	`"file":{"type":"string","description":"the file's key from sift_next, or its path"},` +
	`"base":{"type":"string","description":"the base hash sift_next gave"},` +
	`"content":{"type":"string","description":"the whole recommended file"},` +
	`"findings":{"type":"array","items":{"type":"object","properties":{"row":{"type":"string"},"did":{"type":"string","enum":["fixed","kept"]},"how":{"type":"string","description":"fixed: how, in one line; kept: why, in one line"}},"required":["row","did","how"]}},` +
	`"links":{"type":"array","items":{"type":"string"},"description":"the files this one moves text to or from; each names the other"},` +
	`"summary":{"type":"string","description":"two or three sentences on what changed and why"}},` +
	`"required":["file","base","content","findings","summary"]}}},"required":["recommendations"]}`

// Tools is sift's tool list. None of them decides a file or presses Send:
// those are the user's, on the page.
func Tools() []channelmcp.Tool {
	return []channelmcp.Tool{
		{Name: "sift_check", Description: "Audit the instruction files (each enabled harness's global file and skills, and every instruction file under the configured roots, repos read at their fetched base) and record a new round. Returns the round, what was looked at and skipped, findings per check, and how many files need a recommendation. Then recommend each file with sift_next and sift_propose.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "sift_next", Description: "The next file of the round to recommend: its key, where it is, its budget, its content at the audit with its base hash, its findings (certain ones must be fixed), the other files it may link to, and the guidance for the rewrite. Returns done: true when every file has a recommendation.",
			InputSchema: schema(`{"type":"object","properties":{` + roundProp + `}}`)},
		{Name: "sift_propose", Description: "Store recommendations, all or nothing: per file, the whole revised file, the base hash it started from, each finding as fixed (how) or kept (why), links to the files it moves text to or from (both sides in the same call, each naming the other), and a summary. Refused with the reason when a rule is broken. Returns how many were stored and how many files are left.",
			InputSchema: schema(proposeSchema)},
		{Name: "sift_review", Description: "Open sift's review page in the user's browser and make this session the one their decisions are sent to, once every file has a recommendation. Returns the page URL and how many files wait. Says so, and opens nothing, when there is nothing to review or the round is still being recommended. Use once per round.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "sift_apply", Description: "Apply the round: each file the user accepted or edited and sent is written whole, one branch per repo cut from its fetched base in a worktree. A file whose base changed since the audit is held with the files linked to it; a repo whose primary clone has uncommitted or unpushed work is held, with the reason. With gh and a GitHub remote the branch is pushed and a pull request opened. Returns what happened per repo, the files written and held (with why), what is left for you (files outside a repo, a backlog round's rows), and how many decisions are not sent yet.",
			InputSchema: schema(`{"type":"object","properties":{"round":{"type":"integer","description":"the round to apply (default: the latest)"},"dry_run":{"type":"boolean","description":"work out what would change and touch nothing"}}}`)},
		{Name: "sift_status", Description: "The latest round: its state (recommending, ready, sent, applied), files recommended, open, decided and sent, who the review goes to, and any sends not yet delivered.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "sift_clean", Description: "Remove what sift apply recorded creating, and nothing else: each round branch, local and on origin, once its pull request is merged or closed (without gh, once its commit is in the base), and any worktree apply left behind. Returns the plan, each step remove or keep with why, and with dry_run removes nothing. Run it after the user has merged or closed the round's pull requests.",
			InputSchema: schema(`{"type":"object","properties":{"dry_run":{"type":"boolean","description":"show the plan and remove nothing"}}}`)},
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

// Call runs one tool.
func (ch *Channel) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	text, err := ch.call(ctx, name, args)
	if err != nil {
		return "", errors.New(oneLine(err.Error()))
	}
	return text, nil
}

func (ch *Channel) call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var a struct {
		Round           int64     `json:"round"`
		DryRun          bool      `json:"dry_run"`
		Recommendations []rec.Rec `json:"recommendations"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	switch name {
	case "sift_check":
		return ch.check(ctx)
	case "sift_next":
		return ch.next(ctx, a.Round)
	case "sift_propose":
		return ch.propose(ctx, a.Round, a.Recommendations)
	case "sift_review":
		return ch.review(ctx)
	case "sift_apply":
		return ch.apply(ctx, a.Round, a.DryRun)
	case "sift_status":
		return ch.status(ctx)
	case "sift_clean":
		return ch.clean(ctx, a.DryRun)
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func (ch *Channel) config() (config.Config, error) {
	c, err := ch.LoadConfig()
	if errors.Is(err, config.ErrMissing) {
		return c, errors.New("no sift config: the user needs to run sift init")
	}
	return c, err
}

func (ch *Channel) check(ctx context.Context) (string, error) {
	cfg, err := ch.config()
	if err != nil {
		return "", err
	}
	rep, err := audit.Run(ctx, audit.Options{Config: cfg, LookPath: ch.LookPath, Warn: ch.Log})
	if err != nil {
		return "", err
	}
	certain := 0
	withFindings := map[string]bool{}
	for _, r := range rep.Rows {
		if r.Certain {
			certain++
		}
		withFindings[r.Source.File] = true
	}
	next := "recommend each file: sift_next, then sift_propose, until sift_next says done; then sift_review"
	if len(withFindings) == 0 {
		next = "nothing to recommend: the files are clean"
	}
	return pretty(map[string]any{
		"round": rep.Round, "files": rep.Files, "repos": rep.Repos, "summary": rep.Summary, "warnings": rep.Warnings,
		"skipped": rep.Skipped, "rows": len(rep.Rows), "certain": certain, "files_to_recommend": len(withFindings), "next": next,
	}), nil
}

func (ch *Channel) next(ctx context.Context, round int64) (string, error) {
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	out, err := recommend.Next(ctx, s, round)
	if err != nil {
		return "", err
	}
	return pretty(out), nil
}

func (ch *Channel) propose(ctx context.Context, round int64, recs []rec.Rec) (string, error) {
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	res, err := recommend.Propose(ctx, s, round, recs)
	if err != nil {
		return "", err
	}
	out := map[string]any{"stored": res.Stored, "left": res.Left, "cleared": res.Cleared}
	if res.Left == 0 {
		out["next"] = "every file has a recommendation: run sift_review"
	} else {
		out["next"] = "sift_next for the next file"
	}
	return pretty(out), nil
}

func (ch *Channel) apply(ctx context.Context, round int64, dry bool) (string, error) {
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	o := apply.Options{Store: s, Round: round, DryRun: dry}
	if _, err := ch.LookPath("gh"); err == nil {
		o.Gh = apply.Gh
	}
	if cfg, err := ch.LoadConfig(); err == nil {
		if ps, err := cfg.Enabled(); err == nil {
			o.Instructions = apply.InstructionNames(ps)
		}
	}
	res, err := apply.Run(ctx, o)
	if err != nil {
		return "", err
	}
	return pretty(res), nil
}

func (ch *Channel) clean(ctx context.Context, dry bool) (string, error) {
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		return "", err
	}
	defer func() { _ = s.Close() }()
	o := clean.Options{Store: s, DryRun: dry}
	if _, err := ch.LookPath("gh"); err == nil {
		o.Gh = apply.Gh
	}
	steps, err := clean.Run(ctx, o)
	if err != nil {
		return "", err
	}
	if steps == nil {
		steps = []clean.Step{}
	}
	return pretty(map[string]any{"dry_run": dry, "steps": steps}), nil
}

func (ch *Channel) status(ctx context.Context) (string, error) {
	var out map[string]any
	if _, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/status", nil, &out); err != nil {
		return "", err
	}
	return pretty(out), nil
}

func (ch *Channel) review(ctx context.Context) (string, error) {
	if ch.ID.Session == "" {
		return "", errors.New("this sift channel has no session id (not running under Claude Code or pi), so the user's decisions could not be sent to it")
	}
	var st struct {
		Round int64  `json:"round"`
		State string `json:"state"`
		Open  int    `json:"open"`
	}
	if _, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/status", nil, &st); err != nil {
		return "", err
	}
	if st.Round == 0 || (st.Open == 0 && st.State != string(store.Recommending)) {
		return pretty(map[string]any{"open": 0, "message": "nothing to review"}), nil
	}
	var rv struct {
		URL  string `json:"url"`
		Open int    `json:"open"`
	}
	if _, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/review", map[string]any{"session": ch.ID.Session}, &rv); err != nil {
		return "", err
	}
	res := map[string]any{"url": rv.URL, "open": rv.Open, "opened": true}
	if err := ch.Open(rv.URL); err != nil {
		res["opened"] = false
		res["open_error"] = oneLine(err.Error())
	}
	return pretty(res), nil
}
