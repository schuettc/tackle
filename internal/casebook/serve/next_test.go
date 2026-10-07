package serve

import (
	"net/http"
	"strings"
	"testing"
)

type nextView struct {
	Done bool `json:"done"`
	Left int  `json:"left"`
	Item *struct {
		Item struct {
			Key  string `json:"key"`
			Kind string `json:"kind"`
		} `json:"item"`
		History []any `json:"history"`
	} `json:"item"`
	Choices []struct {
		Disposition string `json:"disposition"`
		Label       string `json:"label"`
		Says        string `json:"says"`
	} `json:"choices"`
	NotNow     []struct{ ID string } `json:"not_now"`
	Guide      string                `json:"guide"`
	ProposeHow string                `json:"propose_how"`
}

type proposeOut struct {
	Proposed  int      `json:"proposed"`
	Errors    []string `json:"errors"`
	Error     string   `json:"error"`
	Proposals []struct {
		Note string `json:"note"`
	} `json:"proposals"`
}

func (r *rig) next(t *testing.T, session string) nextView {
	t.Helper()
	var v nextView
	if c := r.do(t, "GET", "/api/agent/next?session="+session, nil, &v); c != 200 {
		t.Fatalf("next %d", c)
	}
	return v
}

func (r *rig) recommend(t *testing.T, session, key string) {
	t.Helper()
	var out proposeOut
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": session, "keys": []string{key},
		"disposition": "keep", "note": "still active", "from_next": true}, &out); c != 200 || out.Proposed != 1 {
		t.Fatalf("propose %s: %d %+v", key, c, out)
	}
}

// drain walks the queue, recommending each item, and returns the keys in
// the order next handed them out.
func (r *rig) drain(t *testing.T, session string) []string {
	t.Helper()
	var keys []string
	first := r.next(t, session)
	want := first.Left
	v := first
	for !v.Done {
		if v.Item == nil {
			t.Fatalf("not done but no item: %+v", v)
		}
		if v.Left != want {
			t.Fatalf("left %d at %s, want %d", v.Left, v.Item.Item.Key, want)
		}
		keys = append(keys, v.Item.Item.Key)
		r.recommend(t, session, v.Item.Item.Key)
		want--
		if len(keys) > 100 {
			t.Fatal("next never says done")
		}
		v = r.next(t, session)
	}
	if v.Item != nil || v.Left != 0 {
		t.Fatalf("done view %+v", v)
	}
	return keys
}

// TestNextOrderAndDone: waiting on you first, then due, then new; left
// counts down as recommendations land; done with no item at the end.
func TestNextOrderAndDone(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	// A wait that has lapsed: the repo is due.
	var dec DecideResult
	if c := r.do(t, "POST", "/api/decide", map[string]any{"keys": []string{"repo:schuettc/hail"}, "disposition": "wait", "until": "date(2026-01-01)"}, &dec); c != 200 || dec.Decided != 1 {
		t.Fatalf("decide %d %+v", c, dec)
	}
	first := r.next(t, "s1")
	if first.Done || first.Left != 19 {
		t.Fatalf("first next: done %v left %d, want 19 left", first.Done, first.Left)
	}
	if first.Item.Item.Key != "issue:schuettc/hail#10" {
		t.Fatalf("first item %s, want the first waiting item", first.Item.Item.Key)
	}
	if first.Guide == "" || !strings.Contains(first.ProposeHow, "from_next") || len(first.NotNow) != 7 {
		t.Fatalf("next lacks the guide, how to propose or Not now: %+v", first)
	}
	if len(first.Choices) == 0 || first.Choices[0].Label != "Leave it open" || first.Choices[0].Says == "" {
		t.Fatalf("issue choices %+v", first.Choices)
	}
	keys := r.drain(t, "s1")
	if len(keys) != 19 {
		t.Fatalf("handed out %d items, want 19: %v", len(keys), keys)
	}
	if keys[16] != "pr:schuettc/hail#3" || keys[17] != "repo:schuettc/hail" || keys[18] != "branch:schuettc/hail@feat/client" {
		t.Fatalf("order %v: want 17 waiting, then the due repo, then the new branch", keys)
	}
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			t.Fatalf("%s handed out twice: %v", k, keys)
		}
		seen[k] = true
	}
}

// TestNextSkipsDecidedAndProposed: next never hands out a decided item or
// one that already has a pending proposal.
func TestNextSkipsDecidedAndProposed(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	r.decideOne(t, "pr:schuettc/hail#3", "keep")
	r.recommend(t, "s1", "issue:schuettc/hail#10")
	if v := r.next(t, "s1"); v.Left != 17 {
		t.Fatalf("left %d, want 17 (19 less one decided, one proposed)", v.Left)
	}
	for _, k := range r.drain(t, "s1") {
		if k == "pr:schuettc/hail#3" || k == "issue:schuettc/hail#10" {
			t.Fatalf("next handed out %s", k)
		}
	}
}

// TestProposeFromNextNeedsAReason: a recommendation from casebook_next
// must say why.
func TestProposeFromNextNeedsAReason(t *testing.T) {
	r := newRig(t)
	r.attach(t, "s1")
	const key = "issue:schuettc/hail#4"
	var out proposeOut
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{key},
		"disposition": "close", "note": "  ", "from_next": true}, &out); c != http.StatusBadRequest {
		t.Fatalf("no reason: %d %+v, want 400", c, out)
	}
	if out.Error != "a recommendation needs a one-line reason in note" {
		t.Fatalf("error %q", out.Error)
	}
	if v := r.next(t, "s1"); v.Left != 19 {
		t.Fatalf("a refused recommendation was stored: left %d", v.Left)
	}
	out = proposeOut{}
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{key},
		"disposition": "close", "note": "fixed upstream", "from_next": true}, &out); c != 200 || out.Proposed != 1 {
		t.Fatalf("with a reason: %d %+v", c, out)
	}
	if out.Proposals[0].Note != "fixed upstream" {
		t.Fatalf("note %q", out.Proposals[0].Note)
	}
	// Without from_next a note stays optional.
	out = proposeOut{}
	if c := r.do(t, "POST", "/api/agent/propose", map[string]any{"session": "s1", "keys": []string{"issue:schuettc/hail#5"},
		"disposition": "keep"}, &out); c != 200 || out.Proposed != 1 {
		t.Fatalf("plain propose: %d %+v", c, out)
	}
}

// TestNextIsSessionBound: like the other agent endpoints, next needs a
// registered session.
func TestNextIsSessionBound(t *testing.T) {
	r := newRig(t)
	var e struct {
		Code string `json:"code"`
	}
	if c := r.do(t, "GET", "/api/agent/next?session=nobody", nil, &e); c != http.StatusNotFound || e.Code != "unknown_session" {
		t.Fatalf("unknown session: %d %+v, want 404 unknown_session", c, e)
	}
}
