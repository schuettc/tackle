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
	"github.com/schuettc/tackle/internal/sift/config"
	"github.com/schuettc/tackle/internal/sift/store"
	"github.com/schuettc/tools-common/channelmcp"
)

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// Tools is sift's tool list. None of them decides a row or presses Send:
// those are the user's, on the page.
func Tools() []channelmcp.Tool {
	return []channelmcp.Tool{
		{Name: "sift_check", Description: "Audit the instruction files (each enabled harness's global file and skills, and every instruction file under the configured roots, repos read at their fetched base) and record a new round. Returns the round, what was looked at, rows per check, how many rows are certain (sift fixes those itself) and how many need a proposal and the user's decision.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "sift_review", Description: "Open sift's review page in the user's browser and make this session the one their decisions are sent to. Returns the page URL and how many rows wait. Says so, and opens nothing, when there is nothing to review. Use once per round.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "sift_apply", Description: "Apply the round: the certain fixes and the rows the user accepted or edited and sent (delete, rewrite, move, merge), one branch per repo cut from its fetched base in a worktree. A repo whose primary clone has uncommitted or unpushed work is held, with the reason. With gh and a GitHub remote the branch is pushed and a pull request opened. Returns what happened per repo, the rows applied and skipped (with why), the approved rows left for you (issue, global, private, outside a repo), and how many decided rows are not sent yet.",
			InputSchema: schema(`{"type":"object","properties":{"round":{"type":"integer","description":"the round to apply (default: the latest)"},"dry_run":{"type":"boolean","description":"work out what would change and touch nothing"}}}`)},
		{Name: "sift_status", Description: "The latest round: rows open, decided, sent, applied and undone, who the review goes to, and any sends not yet delivered.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
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
		Round  int64 `json:"round"`
		DryRun bool  `json:"dry_run"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	switch name {
	case "sift_check":
		return ch.check(ctx)
	case "sift_review":
		return ch.review(ctx)
	case "sift_apply":
		return ch.apply(ctx, a.Round, a.DryRun)
	case "sift_status":
		return ch.status(ctx)
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
	for _, r := range rep.Rows {
		if r.Certain {
			certain++
		}
	}
	return pretty(map[string]any{
		"round": rep.Round, "files": rep.Files, "repos": rep.Repos, "summary": rep.Summary, "warnings": rep.Warnings,
		"rows": len(rep.Rows), "certain": certain, "to_judge": len(rep.Rows) - certain,
		"next": "propose verdicts for the rows to judge with `sift rows add`, then sift_review",
	}), nil
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
	res, err := apply.Run(ctx, o)
	if err != nil {
		return "", err
	}
	return pretty(res), nil
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
		Round   int64 `json:"round"`
		Open    int   `json:"open"`
		Applied int   `json:"applied"`
	}
	if _, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/status", nil, &st); err != nil {
		return "", err
	}
	if st.Round == 0 || st.Open+st.Applied == 0 {
		return pretty(map[string]any{"open": 0, "message": "nothing to review"}), nil
	}
	var rv struct {
		URL  string `json:"url"`
		Open int    `json:"open"`
	}
	if _, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/review", map[string]any{"session": ch.ID.Session}, &rv); err != nil {
		return "", err
	}
	res := map[string]any{"url": rv.URL, "open": rv.Open, "applied": st.Applied, "opened": true}
	if err := ch.Open(rv.URL); err != nil {
		res["opened"] = false
		res["open_error"] = oneLine(err.Error())
	}
	return pretty(res), nil
}
