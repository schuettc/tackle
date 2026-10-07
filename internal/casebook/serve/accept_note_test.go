package serve

import (
	"net/http"
	"strings"
	"testing"
)

// A recommendation's note is written to Court (the agent's reason). It is
// never a closing comment: accepting a close recommendation records no note
// (the plan's step posts nothing), unless Court gives the comment himself
// with the accept. The note stays on the proposal. Other dispositions post
// nothing, and their accepts keep the note.
const recReason = "A usage question on an example repo no longer maintained."

func (r *rig) acceptOne(t *testing.T, id int64, body map[string]any) {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["ids"] = []int64{id}
	var acc AcceptResult
	if c := r.do(t, "POST", "/api/proposals/accept", body, &acc); c != http.StatusOK || acc.Accepted != 1 {
		t.Fatalf("accept %d: %d %+v", id, c, acc)
	}
}

func (r *rig) planStep(t *testing.T, key string) (command string, posts bool) {
	t.Helper()
	var pv PlanView
	if c := r.do(t, "POST", "/api/apply/plan", map[string]any{"keys": []string{key}}, &pv); c != http.StatusOK {
		t.Fatalf("plan %s: %d", key, c)
	}
	for _, st := range pv.Job.Steps {
		if st.Key == key {
			return st.Command, st.Posts
		}
	}
	t.Fatalf("plan %s: no step for it in %+v", key, pv.Job.Steps)
	return "", false
}

func TestAcceptingACloseRecommendationRecordsNoComment(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#4"
	id := r.proposeOne(t, key, "close", recReason)
	r.acceptOne(t, id, nil)

	d := r.decisionOf(t, key)
	if d == nil || d.Disposition != "close" || d.Note != "" {
		t.Fatalf("decision after accepting the close: %+v, want close with no note", d)
	}
	if !strings.Contains(d.ProposedBy, "s1") {
		t.Errorf("decision ProposedBy %q, want the session's", d.ProposedBy)
	}
	// The reason stays on the proposal.
	p, err := r.s.Props.Get(ctx, id)
	if err != nil || p.Note != recReason {
		t.Errorf("proposal %d note %q (%v), want the reason", id, p.Note, err)
	}
	cmd, posts := r.planStep(t, key)
	if cmd != "gh issue close 4 -R 'schuettc/hail'" || posts {
		t.Errorf("plan step %q posts=%v, want a close with no comment that posts nothing", cmd, posts)
	}
}

func TestAcceptingACloseWithCourtsComment(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#5"
	id := r.proposeOne(t, key, "close", recReason)
	const said = "Thanks, answered in the README."
	r.acceptOne(t, id, map[string]any{"note": said})

	d := r.decisionOf(t, key)
	if d == nil || d.Disposition != "close" || d.Note != said {
		t.Fatalf("decision %+v, want close with Court's comment", d)
	}
	cmd, posts := r.planStep(t, key)
	if cmd != "gh issue close 5 -R 'schuettc/hail' --comment '"+said+"'" || !posts {
		t.Errorf("plan step %q posts=%v, want Court's comment, posted", cmd, posts)
	}
}

func TestAcceptingAKeepRecommendationKeepsItsNote(t *testing.T) {
	r := newRig(t)
	const key = "issue:schuettc/hail#6"
	id := r.proposeOne(t, key, "keep", "active discussion")
	r.acceptOne(t, id, nil)
	if d := r.decisionOf(t, key); d == nil || d.Disposition != "keep" || d.Note != "active discussion" {
		t.Fatalf("decision %+v, want keep with the recommendation's note", d)
	}
}
