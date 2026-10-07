package serve

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/app"
	"github.com/schuettc/tackle/internal/casebook/item"
)

// committed is a decision as decide and accept report committing it, and
// as undo expects to find it.
type committed struct {
	Disposition string    `json:"disposition"`
	Until       string    `json:"until"`
	Note        string    `json:"note"`
	DecidedAt   time.Time `json:"decided_at"`
}

// decideReply is what POST /api/decide answers, with the decision it
// committed per key.
type decideReply struct {
	Decided   int                  `json:"decided"`
	Decisions map[string]committed `json:"decisions"`
}

// pageDecide decides key through the page API and returns the decision the
// reply says it committed.
func (r *rig) pageDecide(t *testing.T, key, disposition, until, note string) committed {
	t.Helper()
	var res decideReply
	body := map[string]any{"keys": []string{key}, "disposition": disposition, "until": until, "note": note}
	if c := r.do(t, "POST", "/api/decide", body, &res); c != 200 || res.Decided != 1 {
		t.Fatalf("decide %s %s: %d %+v", key, disposition, c, res)
	}
	got, ok := res.Decisions[key]
	if !ok || got.Disposition != disposition || got.Until != until || got.Note != note || got.DecidedAt.IsZero() {
		t.Fatalf("decide reply's decision for %s: %+v (%v)", key, got, ok)
	}
	return got
}

// elsewhere decides key as someone else (an agent, a rule, another page).
func (r *rig) elsewhere(t *testing.T, key, disposition, note string) {
	t.Helper()
	if _, _, err := r.App.Decide(context.Background(), key, disposition, app.DecideOptions{Note: note, By: "pi:other", NoPush: true}); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) undo(t *testing.T, key string, expect committed, restore map[string]any) (int, string) {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	c := r.do(t, "POST", "/api/decisions/undo", map[string]any{"key": key, "expect": expect, "restore": restore}, &e)
	return c, e.Error
}

func (r *rig) decisionOf(t *testing.T, key string) *item.Decision {
	t.Helper()
	k, _ := item.ParseKey(key)
	d, err := r.App.Repo.ReadDecision(k)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const undoKey = "pr:schuettc/hail#3"

// proposeOne has an agent recommend disposition for key, returning the
// proposal's id.
func (r *rig) proposeOne(t *testing.T, key, disposition, note string) int64 {
	t.Helper()
	var props struct {
		Proposals []struct{ ID int64 } `json:"proposals"`
		Errors    []string             `json:"errors"`
		Error     string               `json:"error"`
	}
	r.attach(t, "s1")
	r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{key}, "disposition": disposition, "note": note}, &props)
	if len(props.Proposals) != 1 {
		t.Fatalf("propose %+v", props)
	}
	return props.Proposals[0].ID
}

// TestUndoRefusesALaterDecisionElsewhere: someone decided the item in a
// later second; undo is refused and their decision stands.
func TestUndoRefusesALaterDecisionElsewhere(t *testing.T) {
	r := newRig(t)
	mine := r.pageDecide(t, undoKey, "keep", "", "")
	r.Now = r.Now.Add(time.Minute)
	r.elsewhere(t, undoKey, "ignore", "")
	if c, msg := r.undo(t, undoKey, mine, nil); c != http.StatusConflict || msg == "" {
		t.Fatalf("undo after a later decision: %d %q, want 409 with a message", c, msg)
	}
	if d := r.decisionOf(t, undoKey); d == nil || d.Disposition != item.Ignore {
		t.Fatalf("decision after refused undo: %+v", d)
	}
}

// TestUndoRefusesASameSecondChange: a decision made in the same second that
// differs only in disposition, or only in note, still refuses the undo.
func TestUndoRefusesASameSecondChange(t *testing.T) {
	for _, tc := range []struct{ name, disposition, note string }{
		{"disposition", "ignore", "mine"},
		{"note", "keep", "theirs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			mine := r.pageDecide(t, undoKey, "keep", "", "mine")
			r.elsewhere(t, undoKey, tc.disposition, tc.note)
			if c, _ := r.undo(t, undoKey, mine, nil); c != http.StatusConflict {
				t.Fatalf("undo after a same-second change of %s: %d, want 409", tc.name, c)
			}
			d := r.decisionOf(t, undoKey)
			if d == nil || string(d.Disposition) != tc.disposition || d.Note != tc.note {
				t.Fatalf("decision after refused undo: %+v", d)
			}
			// The old clear, given the whole decision, refuses it too.
			if c := r.do(t, "POST", "/api/decisions/clear", map[string]any{"key": undoKey, "decided_at": mine.DecidedAt, "disposition": mine.Disposition, "note": mine.Note}, nil); c != http.StatusConflict {
				t.Fatalf("clear after a same-second change of %s: %d, want 409", tc.name, c)
			}
		})
	}
}

// TestUndoRestoresThePreviousDecision: a re-decide's undo puts back the
// decision the item had, as a new decision.
func TestUndoRestoresThePreviousDecision(t *testing.T) {
	r := newRig(t)
	r.pageDecide(t, undoKey, "keep", "", "was kept")
	r.Now = r.Now.Add(time.Minute)
	mine := r.pageDecide(t, undoKey, "wait", "date(2027-01-01)", "")
	r.Now = r.Now.Add(time.Minute)
	if c, msg := r.undo(t, undoKey, mine, map[string]any{"disposition": "keep", "until": "", "note": "was kept"}); c != 200 {
		t.Fatalf("undo: %d %q", c, msg)
	}
	d := r.decisionOf(t, undoKey)
	if d == nil || d.Disposition != item.Keep || d.Until != "" || d.Note != "was kept" || !d.DecidedAt.Equal(r.Now.UTC().Truncate(time.Second)) {
		t.Fatalf("decision after undo: %+v", d)
	}
	if it, _ := r.s.Index.Item(undoKey); it.Decision == nil || it.Decision.Disposition != item.Keep {
		t.Fatalf("index after undo: %+v", it.Decision)
	}
}

// TestUndoWithNullRestoreClears: undo of a first decision removes it.
func TestUndoWithNullRestoreClears(t *testing.T) {
	r := newRig(t)
	mine := r.pageDecide(t, undoKey, "keep", "", "")
	if c, msg := r.undo(t, undoKey, mine, nil); c != 200 {
		t.Fatalf("undo: %d %q", c, msg)
	}
	if d := r.decisionOf(t, undoKey); d != nil {
		t.Fatalf("decision after undo: %+v", d)
	}
	if it, _ := r.s.Index.Item(undoKey); it.Decision != nil {
		t.Fatalf("index after undo: %+v", it.Decision)
	}
}

// TestAcceptReportsTheDecisionItCommitted: undo after accepting a
// recommendation expects what accept committed.
func TestAcceptReportsTheDecisionItCommitted(t *testing.T) {
	r := newRig(t)
	const acceptKey = "issue:schuettc/hail#4"
	id := r.proposeOne(t, acceptKey, "keep", "still active")
	var res struct {
		Accepted  int                  `json:"accepted"`
		Decisions map[string]committed `json:"decisions"`
	}
	if c := r.do(t, "POST", "/api/proposals/accept", map[string]any{"ids": []int64{id}}, &res); c != 200 || res.Accepted != 1 {
		t.Fatalf("accept: %d %+v", c, res)
	}
	got := res.Decisions[acceptKey]
	if got.Disposition != "keep" || got.Note != "still active" || !got.DecidedAt.Equal(r.Now.UTC().Truncate(time.Second)) {
		t.Fatalf("accept reply's decision: %+v", got)
	}
	if c, msg := r.undo(t, acceptKey, got, nil); c != 200 {
		t.Fatalf("undo of the accepted decision: %d %q", c, msg)
	}
}
