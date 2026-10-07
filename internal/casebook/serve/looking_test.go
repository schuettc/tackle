package serve

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// lookingOf reads key's looking field from GET /api/item and from its row
// in GET /api/items?view=all.
func (r *rig) lookingOf(t *testing.T, key string) (detail, row *Looking) {
	t.Helper()
	var d ItemDetailView
	if c := r.do(t, "GET", "/api/item?key="+url.QueryEscape(key), nil, &d); c != http.StatusOK {
		t.Fatalf("GET /api/item %s: %d", key, c)
	}
	var iv ItemsView
	if c := r.do(t, "GET", "/api/items?view=all&limit=500", nil, &iv); c != http.StatusOK {
		t.Fatalf("GET /api/items: %d", c)
	}
	for _, it := range append(iv.Items, iv.LeftOpen...) {
		if it.ID == key {
			row = it.Looking
		}
	}
	return d.Item.Looking, row
}

// lookInto posts "ask ‹session› to look into it": the vocabulary's message
// for key, key attached, with the purpose the page sends.
func (r *rig) lookInto(t *testing.T, th int64, key string) message {
	t.Helper()
	var m message
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": LookIntoText(key), "attached": map[string]any{"keys": []string{key}}, "purpose": PurposeLookInto}, &m); c != 200 {
		t.Fatalf("post %d", c)
	}
	return m
}

// "ask ‹session› to look into it" sends a normal message with the
// look-into purpose, the item attached. While it is queued or being
// worked on, the item (and its row) says which session is looking into
// it; settled, it doesn't.
func TestLookingIntoFollowsTheMessage(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	key := "issue:schuettc/hail#4"
	other := "issue:schuettc/hail#5"

	if d, row := r.lookingOf(t, key); d != nil || row != nil {
		t.Fatalf("before any message: %+v %+v", d, row)
	}
	m := r.lookInto(t, th, key)
	d, row := r.lookingOf(t, key)
	if d == nil || row == nil || d.Session.ID != "s1" || d.Message != m.ID || row.Session.ID != "s1" {
		t.Fatalf("queued: detail %+v row %+v, want s1 looking (message %d)", d, row, m.ID)
	}
	if d, row := r.lookingOf(t, other); d != nil || row != nil {
		t.Fatalf("another item marked: %+v %+v", d, row)
	}

	// Delivered and being worked on: still looking.
	var w waited
	if c := r.do(t, "GET", "/api/agent/wait?session=s1", nil, &w); c != 200 {
		t.Fatalf("wait %d", c)
	}
	if d, _ := r.lookingOf(t, key); d == nil {
		t.Fatal("delivered: no longer looking")
	}
	// Answered: settled, the marker goes.
	var ids []int64
	for _, x := range w.Delivery.Messages {
		ids = append(ids, x.ID)
	}
	if c := r.do(t, "POST", "/api/agent/reply", map[string]any{"session": "s1", "ids": ids, "state": "answered", "text": "looked"}, nil); c != 200 {
		t.Fatalf("reply %d", c)
	}
	if d, row := r.lookingOf(t, key); d != nil || row != nil {
		t.Fatalf("answered: still looking %+v %+v", d, row)
	}
}

// A message Court types himself, with the very same words and the item
// attached, is just a message: only the purpose the button sends marks the
// item.
func TestHandTypedLookIntoMarksNothing(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	key := "issue:schuettc/hail#4"
	var m message
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": LookIntoText(key), "attached": map[string]any{"keys": []string{key}}}, &m); c != 200 {
		t.Fatalf("post %d", c)
	}
	if d, row := r.lookingOf(t, key); d != nil || row != nil {
		t.Fatalf("a hand-typed message marked the item: %+v %+v", d, row)
	}
	// The purpose with no item attached marks nothing either, and serve
	// refuses a purpose it doesn't know.
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": "look", "purpose": PurposeLookInto}, nil); c != http.StatusBadRequest {
		t.Fatalf("look-into with no item attached: %d, want 400", c)
	}
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": "x", "attached": map[string]any{"keys": []string{key}}, "purpose": "nonsense"}, nil); c != http.StatusBadRequest {
		t.Fatalf("an unknown purpose: %d, want 400", c)
	}
	if d, _ := r.lookingOf(t, key); d != nil {
		t.Fatalf("marked: %+v", d)
	}
}

// The ask's words are serve's: the decision vocabulary carries the button's
// label and the message, each with its hole, and LookIntoText fills the
// message in (verbatim).
func TestLookIntoTextIsInTheVocabulary(t *testing.T) {
	want := "Look into pr:o/r#1: check its CI, recent activity and anything blocking it. If a check is failing, find the cause and what would fix it. Add what you find as evidence (casebook_evidence) and recommend what to do with a one-line reason (casebook_propose)."
	if got := LookIntoText("pr:o/r#1"); got != want {
		t.Fatalf("LookIntoText = %q", got)
	}
	r := newRig(t)
	var v DecisionVocabView
	if c := r.do(t, "GET", "/api/decisions/vocabulary", nil, &v); c != 200 {
		t.Fatalf("vocabulary %d", c)
	}
	if v.LookInto.Label != "ask {session} to look into it" {
		t.Fatalf("label %q", v.LookInto.Label)
	}
	if got := strings.ReplaceAll(v.LookInto.Message, "{key}", "pr:o/r#1"); got != want {
		t.Fatalf("message %q", v.LookInto.Message)
	}
}
