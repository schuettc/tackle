package serve

import (
	"net/http"
	"net/url"
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

// "ask ‹session› to look into it" sends a normal message: LookIntoText,
// the item attached. While it is queued or being worked on, the item (and
// its row) says which session is looking into it; settled, it doesn't.
func TestLookingIntoFollowsTheMessage(t *testing.T) {
	r := newRig(t)
	th := r.attach(t, "s1")
	key := "issue:schuettc/hail#4"
	other := "issue:schuettc/hail#5"

	if d, row := r.lookingOf(t, key); d != nil || row != nil {
		t.Fatalf("before any message: %+v %+v", d, row)
	}
	// A message about the item that isn't the ask marks nothing; nor does
	// the ask's text with the item not attached.
	r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": "what about this one?", "attached": map[string]any{"keys": []string{key}}}, nil)
	r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": LookIntoText(key)}, nil)
	if d, row := r.lookingOf(t, key); d != nil || row != nil {
		t.Fatalf("other messages marked it: %+v %+v", d, row)
	}

	var m message
	if c := r.do(t, "POST", "/api/messages", map[string]any{"thread": th, "body": LookIntoText(key), "attached": map[string]any{"keys": []string{key}}}, &m); c != 200 {
		t.Fatalf("post %d", c)
	}
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

// The ask's text, verbatim (the page sends the same: attention.ts).
func TestLookIntoText(t *testing.T) {
	want := "Look into pr:o/r#1: check its CI, recent activity and anything blocking it. If a check is failing, find the cause and what would fix it. Add what you find as evidence (casebook_evidence) and recommend what to do with a one-line reason (casebook_propose)."
	if got := LookIntoText("pr:o/r#1"); got != want {
		t.Fatalf("LookIntoText = %q", got)
	}
}
