package check

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/judge"
	"github.com/schuettc/tackle/internal/cull/store"
)

// GroupHash identifies a group's exact content: the SHA-256 of its member
// hashes in order. A group answer sticks only while no member changes.
func GroupHash(memberHashes []string) string {
	sum := sha256.Sum256([]byte(strings.Join(memberHashes, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// eligible reports whether a result is one check would send to review.
func eligible(verdict, rule string) bool {
	return verdict == "review" && (rule == "review_band" || rule == "truncated" || rule == "pins_setting")
}

// applyAnswers settles eligible review results with Court's stored answers
// (matched by item id and hash) and returns how many it settled. Jev's
// confident verdicts are never overridden, and an answer whose value does
// not fit the item's kind is ignored.
func applyAnswers(tests []TestResult, groups []GroupResult, as map[store.Key]store.Answer) (answered int) {
	withNote := func(reasons []string, note string) []string {
		if note == "" {
			return reasons
		}
		return append(append([]string(nil), reasons...), "court: "+note)
	}
	for i := range tests {
		t := &tests[i]
		if t.Err != "" || !eligible(t.Verdict, t.Rule) {
			continue
		}
		a, ok := as[store.Key{ItemID: t.ID, Hash: t.Hash}]
		if !ok || (a.Value != "cut" && a.Value != "keep") {
			continue
		}
		t.Reasons = withNote(t.Reasons, a.Note)
		t.Verdict, t.Rule = a.Value, "court"
		answered++
	}
	for i := range groups {
		g := &groups[i]
		if g.Err != "" || !eligible(g.Verdict, g.Rule) {
			continue
		}
		a, ok := as[store.Key{ItemID: g.ID, Hash: GroupHash(g.MemberHashes)}]
		if !ok {
			continue
		}
		var verdict string
		switch a.Value {
		case "merge":
			verdict = "consolidate"
		case "separate":
			verdict = "keep_separate"
		default:
			continue
		}
		g.Reasons = withNote(g.Reasons, a.Note)
		g.Verdict, g.Rule = verdict, "court"
		answered++
	}
	return answered
}

// reviewStore is check's best-effort handle on the review database. A nil
// store means "unavailable": every method is then a no-op.
type reviewStore struct {
	s     *store.Store
	owned bool
	proj  store.Project
}

func openReviewStore(ctx context.Context, given *store.Store, stderr io.Writer) *reviewStore {
	if given != nil {
		return &reviewStore{s: given}
	}
	s, err := store.Open(ctx, store.Path())
	if err != nil {
		warnStore(stderr, err)
		return &reviewStore{}
	}
	return &reviewStore{s: s, owned: true}
}

func warnStore(stderr io.Writer, err error) {
	_, _ = fmt.Fprintf(stderr, "cull: review store unavailable: %v\n", err)
}

func (r *reviewStore) close() {
	if r.s != nil && r.owned {
		_ = r.s.Close()
	}
}

// answers registers the project and returns its stored answers; on any
// error it warns, disables the store and returns none.
func (r *reviewStore) answers(ctx context.Context, root string, stderr io.Writer) map[store.Key]store.Answer {
	if r.s == nil {
		return nil
	}
	p, err := r.s.Project(ctx, root)
	if err == nil {
		var as map[store.Key]store.Answer
		if as, err = r.s.Answers(ctx, p.ID); err == nil {
			r.proj = p
			return as
		}
	}
	warnStore(stderr, err)
	r.s = nil
	return nil
}

func (r *reviewStore) record(ctx context.Context, rep Report, items []store.Item, questionsHash string, total int, stderr io.Writer) {
	if r.s == nil {
		return
	}
	run := store.Run{ProjectID: r.proj.ID, Mode: rep.Mode, Base: rep.Base, QuestionsHash: questionsHash, Total: total, Summary: rep.Summary}
	if _, err := r.s.RecordRun(ctx, run, items); err != nil {
		warnStore(stderr, err)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // states and answers are plain data, as in judge.StateHash
	}
	return b
}

func recorded(rule string) bool {
	return rule == "review_band" || rule == "truncated" || rule == "pins_setting" || rule == "court"
}

// recordItems builds the run's items: every result (no error) whose rule is
// review_band, truncated, pins_setting or court, with the exact state judged and Jev's
// answers.
func recordItems(kept []cases.TestCase, tj []judge.Judged, tests []TestResult, gStates []any, gj []judge.Judged, gout []GroupResult) []store.Item {
	var items []store.Item
	for i, tr := range tests {
		if tr.Err != "" || !recorded(tr.Rule) {
			continue
		}
		items = append(items, store.Item{
			ID: tr.ID, Kind: "test", Hash: tr.Hash, File: tr.File, Name: tr.Name,
			Verdict: tr.Verdict, Rule: tr.Rule,
			State: mustJSON(judge.StateFor(kept[i])), Jev: mustJSON(tj[i].Answers), Model: tj[i].Model,
		})
	}
	for i, gr := range gout {
		if gr.Err != "" || !recorded(gr.Rule) {
			continue
		}
		items = append(items, store.Item{
			ID: gr.ID, Kind: "group", Hash: GroupHash(gr.MemberHashes), File: gr.File, Name: strings.Join(gr.Members, ", "),
			Verdict: gr.Verdict, Rule: gr.Rule,
			State: mustJSON(gStates[i]), Jev: mustJSON(gj[i].Answers), Model: gj[i].Model,
			Rows: gr.Rows, Members: gr.Members,
		})
	}
	return items
}
