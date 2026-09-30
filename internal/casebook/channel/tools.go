package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/serve"
	"github.com/schuettc/tools-common/channelmcp"
)

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// Tools is casebook's tool list (casebook workbench spec §6.4, P1 subset).
func Tools() []channelmcp.Tool {
	return []channelmcp.Tool{
		{Name: "casebook_status", Description: "The attention counts, whether Court has the casebook page open, and what changed since you last looked: how many of your proposals Court accepted, and each one he changed or rejected with his reason.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "casebook_open", Description: "Open the casebook page in Court's browser, straight at one item (key) or an Attention view (view), or at the front with neither. Only when he asks, or when you hand him something to review; never repeatedly.",
			InputSchema: schema(`{"type":"object","properties":{"key":{"type":"string","description":"an item key: open at that item"},"view":{"type":"string","enum":["waiting","new","due","proposed","all","board"],"description":"open at this Attention view"}}}`)},
		{Name: "casebook_attention", Description: "List items needing attention. view: waiting (incoming, no reply from Court), new (undecided), due (due/drift/conflict), proposed (a proposal is pending), all.",
			InputSchema: schema(`{"type":"object","properties":{"view":{"type":"string","enum":["waiting","new","due","proposed","all"]},"kind":{"type":"string","enum":["repo","pr","issue","branch","worktree"]},"repo":{"type":"string","description":"owner/name or owner"},"q":{"type":"string","description":"substring of key or title"},"offset":{"type":"integer"},"limit":{"type":"integer"}}}`)},
		{Name: "casebook_show", Description: "One item: its observation, decision, pending proposal, evidence and recent history.",
			InputSchema: schema(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`)},
		{Name: "casebook_history", Description: "Every journaled action and decision commit for an item.",
			InputSchema: schema(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`)},
		{Name: "casebook_propose", Description: "Propose a decision for one or more items. Court accepts, changes or rejects it on the page; you never decide. disposition: keep, archive, close, delete, merge, wait, watch, ignore (wait and watch need until: date(YYYY-MM-DD), merged(<pr>), closed(<pr|issue>), inactive(90d), released(<repo>)). A newer proposal from you for the same item replaces the older one.",
			InputSchema: schema(`{"type":"object","properties":{"keys":{"type":"array","items":{"type":"string"}},"disposition":{"type":"string"},"until":{"type":"string"},"note":{"type":"string","description":"why, shown to Court"}},"required":["keys","disposition"]}`)},
		{Name: "casebook_evidence", Description: "Attach a finding to an item (shown on the page under evidence, attributed to you).",
			InputSchema: schema(`{"type":"object","properties":{"key":{"type":"string"},"text":{"type":"string"}},"required":["key","text"]}`)},
		{Name: "casebook_progress", Description: "Set your live progress line on the page while you work (replaces the previous one).",
			InputSchema: schema(`{"type":"object","properties":{"text":{"type":"string"},"n":{"type":"integer"},"total":{"type":"integer"}},"required":["text"]}`)},
		{Name: "casebook_reply", Description: "Settle Court's messages by id (the number in [m-N]). state: received, working, answered, declined or failed. text is your reply, shown in the thread. Every delivered message must end answered, declined or failed. You may also settle a message from a previous turn that was left unanswered; use answered, declined or failed.",
			InputSchema: schema(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"integer"}},"state":{"type":"string","enum":["received","working","answered","declined","failed"]},"text":{"type":"string"}},"required":["ids","state"]}`)},
		{Name: "casebook_rule_draft", Description: "Create or update a draft rule (Court activates it; you never can). Provide a rule object with id, name, match conditions and a propose block. status must be \"draft\" or omitted; setting it to \"active\" is refused.",
			InputSchema: schema(`{"type":"object","properties":{"id":{"type":"string","description":"rule id: lower-case letters, digits and hyphens"},"name":{"type":"string"},"match":{"type":"array","items":{"type":"object","properties":{"Field":{"type":"string"},"Op":{"type":"string"},"Value":{"type":"string"}}}},"propose":{"type":"object","properties":{"Disposition":{"type":"string"},"Until":{"type":"string"},"Note":{"type":"string"}}},"note":{"type":"string","description":"a short note about what this rule is for"}},"required":["id","name","match","propose"]}`)},
		{Name: "casebook_job_step", Description: "Report an apply job step's progress. state: started (beginning work on the step), reported (command dispatched — casebook verifies the outcome), paused (precondition failed; provide detail), failed (command error; provide detail). Only the session the job was approved for may call this.",
			InputSchema: schema(`{"type":"object","properties":{"job":{"type":"integer","description":"job id"},"step":{"type":"integer","description":"step id"},"state":{"type":"string","enum":["started","reported","paused","failed"]},"detail":{"type":"string","description":"reason for paused or failed"}},"required":["job","step","state"]}`)},
		{Name: "casebook_job_ask", Description: "Draft public text for an apply step that posts a comment (Posts=true), or ask Court a question mid-job. Opens a needs-you card that Court reviews on the page. Nothing is posted until Court approves. Only the session the job was approved for may call this.",
			InputSchema: schema(`{"type":"object","properties":{"job":{"type":"integer","description":"job id"},"step":{"type":"integer","description":"step id"},"question":{"type":"string","description":"what you are asking Court to review or approve"},"text":{"type":"string","description":"draft text to post (for posts=true steps) or a question body"}},"required":["job","step","question"]}`)},
	}
}

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// Call runs one tool.
func (ch *Channel) Call(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var a struct {
		View        string   `json:"view"`
		Kind        string   `json:"kind"`
		Repo        string   `json:"repo"`
		Q           string   `json:"q"`
		Offset      int      `json:"offset"`
		Limit       int      `json:"limit"`
		Key         string   `json:"key"`
		Keys        []string `json:"keys"`
		Disposition string   `json:"disposition"`
		Until       string   `json:"until"`
		Note        string   `json:"note"`
		Text        string   `json:"text"`
		N           int      `json:"n"`
		Total       int      `json:"total"`
		IDs         []int64  `json:"ids"`
		State       string   `json:"state"`
		Job         int64    `json:"job"`
		Step        int64    `json:"step"`
		Detail      string   `json:"detail"`
		Question    string   `json:"question"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return "", fmt.Errorf("bad arguments: %w", err)
		}
	}
	needSession := func() error {
		if ch.ID.Session == "" {
			return errors.New("this casebook channel has no session id (not running under Claude Code or pi), so it can only read")
		}
		return nil
	}
	switch name {
	case "casebook_status":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodGet, "/api/agent/status?"+q("session", ch.ID.Session), nil, &out)
			return pretty(out), err
		})
	case "casebook_open":
		_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/open", map[string]any{"key": a.Key, "view": a.View}, nil)
		if err != nil {
			return "", err
		}
		switch {
		case a.Key != "":
			return "opened the casebook page at " + a.Key, nil
		case a.View != "":
			return "opened the casebook page at the " + a.View + " view", nil
		}
		return "opened the casebook page", nil
	case "casebook_attention":
		return ch.attention(ctx, serve.Query{View: a.View, Kind: a.Kind, Repo: a.Repo, Text: a.Q, Offset: a.Offset, Limit: a.Limit})
	case "casebook_show":
		return ch.show(ctx, a.Key)
	case "casebook_history":
		return ch.history(ctx, a.Key)
	case "casebook_propose":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/propose", map[string]any{"session": ch.ID.Session, "keys": a.Keys,
				"disposition": a.Disposition, "until": a.Until, "note": a.Note}, &out)
			return pretty(out), err
		})
	case "casebook_evidence":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/evidence", map[string]any{"session": ch.ID.Session, "key": a.Key, "text": a.Text}, &out)
			return pretty(out), err
		})
	case "casebook_progress":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/progress", map[string]any{"session": ch.ID.Session, "text": a.Text, "n": a.N, "total": a.Total}, nil)
			return "ok", err
		})
	case "casebook_reply":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/reply", map[string]any{"session": ch.ID.Session, "ids": a.IDs, "state": a.State, "text": a.Text}, &out)
			return pretty(out), err
		})
	case "casebook_rule_draft":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			// Parse the tool's own schema fields (lowercase) and translate to the
			// Go field names the server's rules.Rule JSON decoder expects.
			var rd struct {
				ID      string                       `json:"id"`
				Name    string                       `json:"name"`
				Match   []map[string]json.RawMessage `json:"match"`
				Propose map[string]json.RawMessage   `json:"propose"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &rd); err != nil {
					return "", fmt.Errorf("bad rule_draft arguments: %w", err)
				}
			}
			// Build match conditions using snake_case json keys (Fix 2: rules structs
			// now have json tags matching the TOML names). Accept both casings from
			// the tool caller so the MCP schema stays backward-compatible.
			match := make([]map[string]any, 0, len(rd.Match))
			for _, m := range rd.Match {
				cond := map[string]any{}
				condKeyMap := [][2]string{{"Field", "field"}, {"field", "field"}, {"Op", "op"}, {"op", "op"}, {"Value", "value"}, {"value", "value"}}
				for _, kk := range condKeyMap {
					if v, ok := m[kk[0]]; ok {
						var s string
						if err := json.Unmarshal(v, &s); err == nil {
							cond[kk[1]] = s
						}
					}
				}
				match = append(match, cond)
			}
			// Build propose using snake_case keys.
			propose := map[string]any{}
			propKeyMap := [][2]string{{"Disposition", "disposition"}, {"disposition", "disposition"}, {"Until", "until"}, {"until", "until"}, {"Note", "note"}, {"note", "note"}}
			for _, kk := range propKeyMap {
				if v, ok := rd.Propose[kk[0]]; ok {
					var s string
					if err := json.Unmarshal(v, &s); err == nil {
						propose[kk[1]] = s
					}
				}
			}
			body := map[string]any{
				"session": ch.ID.Session,
				"rule": map[string]any{
					"id":      rd.ID,
					"name":    rd.Name,
					"status":  "draft",
					"match":   match,
					"propose": propose,
				},
			}
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/rule-draft", body, &out)
			if err != nil {
				return "", err
			}
			if errMsg, ok := out["error"].(string); ok {
				return "", fmt.Errorf("%s", errMsg)
			}
			if rule, ok := out["rule"].(map[string]any); ok {
				return fmt.Sprintf("draft rule %q written (created_by %q)", rule["id"], rule["created_by"]), nil
			}
			return pretty(out), nil
		})
	case "casebook_job_step":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			body := map[string]any{
				"session": ch.ID.Session,
				"job":     a.Job,
				"step":    a.Step,
				"state":   a.State,
				"detail":  a.Detail,
			}
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/job-step", body, &out)
			if err != nil {
				return "", err
			}
			return pretty(out), nil
		})
	case "casebook_job_ask":
		if err := needSession(); err != nil {
			return "", err
		}
		return ch.callSessionBound(ctx, func() (string, error) {
			body := map[string]any{
				"session":  ch.ID.Session,
				"job":      a.Job,
				"step":     a.Step,
				"question": a.Question,
				"text":     a.Text,
			}
			var out map[string]any
			_, err := ch.Client.Do(ctx, http.MethodPost, "/api/agent/job-ask", body, &out)
			if err != nil {
				return "", err
			}
			return pretty(out), nil
		})
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func line(it serve.ItemView) string {
	s := fmt.Sprintf("%-9s %s", it.Status, it.ID)
	if it.Title != "" {
		s += " · " + it.Title
	}
	if len(it.Hits) > 0 {
		s += " · " + it.Hits[0].Detail
	} else if len(it.Evidence) > 0 {
		s += " · " + it.Evidence[0]
	}
	if it.Proposal != nil {
		s += fmt.Sprintf(" · proposed %s by %s", it.Proposal.Disposition, it.Proposal.Source)
	}
	return s
}

func (ch *Channel) attention(ctx context.Context, qy serve.Query) (string, error) {
	if qy.View == "" {
		qy.View = serve.ViewAll
	}
	var out struct {
		Total int              `json:"total"`
		Items []serve.ItemView `json:"items"`
	}
	_, err := ch.Client.Do(ctx, http.MethodGet, "/api/items?"+q("view", qy.View, "kind", qy.Kind, "repo", qy.Repo, "q", qy.Text,
		"offset", strconv.Itoa(qy.Offset), "limit", strconv.Itoa(qy.Limit)), nil, &out)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d item(s) in %s", out.Total, qy.View)
	if len(out.Items) < out.Total {
		fmt.Fprintf(&b, " (showing %d from offset %d)", len(out.Items), qy.Offset)
	}
	b.WriteString("\n")
	for _, it := range out.Items {
		b.WriteString(line(it) + "\n")
	}
	return b.String(), nil
}

func (ch *Channel) show(ctx context.Context, key string) (string, error) {
	k, err := item.ParseKey(key)
	if err != nil {
		return "", err
	}
	var out map[string]any
	_, err = ch.Client.Do(ctx, http.MethodGet, "/api/item?"+q("key", k.String()), nil, &out)
	return pretty(out), err
}

func (ch *Channel) history(ctx context.Context, key string) (string, error) {
	k, err := item.ParseKey(key)
	if err != nil {
		return "", err
	}
	var out struct {
		History   json.RawMessage `json:"history"`
		Decisions json.RawMessage `json:"decisions"`
	}
	if _, err := ch.Client.Do(ctx, http.MethodGet, "/api/item?"+q("key", k.String()), nil, &out); err != nil {
		return "", err
	}
	return pretty(map[string]any{"decisions": out.Decisions, "events": out.History}), nil
}
