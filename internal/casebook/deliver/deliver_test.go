package deliver

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schuettc/tackle/internal/casebook/db"
)

var ctx = context.Background()

var update = flag.Bool("update", false, "rewrite golden files")

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

func newQueue(t *testing.T) (*Queue, *clock) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "casebook.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	c := &clock{t: time.Date(2026, 9, 27, 9, 40, 0, 0, time.UTC)}
	q := New(d)
	q.Now = c.now
	if err := q.Touch(ctx, Session{ID: "s1", Harness: "pi", Label: "pi · tools-workspace", CWD: "/w", PID: 42}); err != nil {
		t.Fatal(err)
	}
	return q, c
}

func thread(t *testing.T, q *Queue, session string) Thread {
	t.Helper()
	th, err := q.NewThread(ctx, session, "triage")
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func post(t *testing.T, q *Queue, th Thread, body string, batch bool) Message {
	t.Helper()
	m, err := q.Post(ctx, th.ID, body, Attached{}, batch)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func ids(ms []Message) []int64 {
	var out []int64
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

func TestOneInFlightAndMidTurnMessagesWait(t *testing.T) {
	q, c := newQueue(t)
	th := thread(t, q, "s1")
	m1 := post(t, q, th, "propose decisions for the 40 chime PRs", false)
	d1, err := q.Next(ctx, "s1")
	if err != nil || d1 == nil || len(d1.Messages) != 1 || d1.Messages[0].ID != m1.ID || d1.Messages[0].State != Delivered {
		t.Fatalf("first delivery %+v %v", d1, err)
	}
	// Mid-turn: two more messages arrive; nothing goes out.
	c.add(time.Minute)
	m2 := post(t, q, th, "merge these if CI is green", false)
	c.add(time.Minute)
	m3 := post(t, q, th, "actually skip #673", false)
	if d, _ := q.Next(ctx, "s1"); d != nil {
		t.Fatalf("delivered while a delivery is in flight: %+v", d)
	}
	if got, _ := q.Message(ctx, m2.ID); got.State != Queued {
		t.Fatalf("m2 state %s", got.State)
	}
	// The agent settles m1; the turn's delivery ends; the queue drains in order.
	if _, _, err := q.Reply(ctx, "s1", []int64{m1.ID}, Answered, "38 close, 2 keep"); err != nil {
		t.Fatal(err)
	}
	if d, _ := q.Delivery(ctx, d1.ID); d.State != Done {
		t.Fatalf("delivery state %s", d.State)
	}
	d2, _ := q.Next(ctx, "s1")
	if d2 == nil || len(d2.Messages) != 2 || d2.Messages[0].ID != m2.ID || d2.Messages[1].ID != m3.ID {
		t.Fatalf("second delivery %+v", d2)
	}
	// The agent's reply landed in the thread.
	all, _ := q.Messages(ctx, th.ID)
	var reply *Message
	for i := range all {
		if all[i].State == AgentReply {
			reply = &all[i]
		}
	}
	if reply == nil || reply.Author != "s1" || reply.ReplyTo != m1.ID || reply.Body != "38 close, 2 keep" {
		t.Fatalf("reply %+v in %+v", reply, all)
	}
}

func TestBatchesStayIntactAndOrdered(t *testing.T) {
	q, c := newQueue(t)
	th := thread(t, q, "s1")
	a := post(t, q, th, "first draft", true)
	b := post(t, q, th, "second draft", true)
	single := post(t, q, th, "sent now", false)
	bid, drafts, _ := q.DraftBatch(ctx, th.ID)
	if bid == 0 || len(drafts) != 2 || drafts[0].BatchPos != 1 || drafts[1].BatchPos != 2 {
		t.Fatalf("drafts %+v", drafts)
	}
	if err := q.ReorderBatch(ctx, bid, []int64{b.ID, a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := q.EditDraft(ctx, a.ID, "first draft, edited"); err != nil {
		t.Fatal(err)
	}
	c.add(time.Minute)
	if n, err := q.SendBatch(ctx, bid); err != nil || n != 2 {
		t.Fatalf("send %d %v", n, err)
	}
	d, _ := q.Next(ctx, "s1")
	got := ids(d.Messages)
	if len(got) != 3 || got[0] != single.ID || got[1] != b.ID || got[2] != a.ID {
		t.Fatalf("order %v (want single, b, a)", got)
	}
	if d.Messages[2].Body != "first draft, edited" {
		t.Fatalf("edit lost: %q", d.Messages[2].Body)
	}
	if bid2, _, _ := q.DraftBatch(ctx, th.ID); bid2 != 0 {
		t.Fatalf("draft batch remains %d", bid2)
	}
}

func TestSettledEndsTurnAndMarksUnanswered(t *testing.T) {
	q, _ := newQueue(t)
	th := thread(t, q, "s1")
	m1 := post(t, q, th, "one", false)
	m2 := post(t, q, th, "two", false)
	d, _ := q.Next(ctx, "s1")
	q.Reply(ctx, "s1", []int64{m1.ID}, Answered, "")
	q.Reply(ctx, "s1", []int64{m2.ID}, Working, "")
	// Pass the delivery id so Settled knows it was shown (updated from old unconditional call).
	ended, err := q.Settled(ctx, "s1", []int64{d.ID})
	if err != nil || ended == nil || ended.State != Done {
		t.Fatalf("settled %+v %v", ended, err)
	}
	if got, _ := q.Message(ctx, m2.ID); got.State != Unanswered {
		t.Fatalf("m2 %s", got.State)
	}
	if got, _ := q.Message(ctx, m1.ID); got.State != Answered {
		t.Fatalf("m1 %s", got.State)
	}
	if again, _ := q.Settled(ctx, "s1", nil); again != nil {
		t.Fatalf("second settle %+v", again)
	}
	if n, _ := q.Resend(ctx, []int64{m2.ID, m1.ID}); n != 1 {
		t.Fatalf("resend %d (only the unanswered one)", n)
	}
	d2, _ := q.Next(ctx, "s1")
	if d2 == nil || d2.ID == d.ID || len(d2.Messages) != 1 || d2.Messages[0].ID != m2.ID {
		t.Fatalf("resent delivery %+v", d2)
	}
}

// TestSettledLeavesUnshownDeliveryInFlight verifies that a turn's end does NOT
// end a delivery the agent was never shown.
func TestSettledLeavesUnshownDeliveryInFlight(t *testing.T) {
	q, _ := newQueue(t)
	th := thread(t, q, "s1")
	m := post(t, q, th, "one", false)
	q.Next(ctx, "s1") // delivery is now inflight

	// Settle without showing the delivery id → must stay in flight.
	ended, err := q.Settled(ctx, "s1", nil)
	if err != nil {
		t.Fatalf("settled err: %v", err)
	}
	if ended != nil {
		t.Fatalf("Settled returned non-nil when delivery was unshown: %+v", ended)
	}
	// Message still delivered (in flight), not unanswered.
	got, _ := q.Message(ctx, m.ID)
	if got.State != Delivered {
		t.Fatalf("message state %s, want %s", got.State, Delivered)
	}
	// Inflight delivery is still there.
	d, err := q.Inflight(ctx, "s1")
	if err != nil || d == nil {
		t.Fatalf("inflight after unshown settle: %v %v", d, err)
	}
}

// TestSettledWithShownIDEndsDelivery verifies that a delivery is ended when
// its id is passed in shownIDs.
func TestSettledWithShownIDEndsDelivery(t *testing.T) {
	q, _ := newQueue(t)
	th := thread(t, q, "s1")
	m := post(t, q, th, "one", false)
	d, _ := q.Next(ctx, "s1")

	// First settle without shown → stays in flight.
	if ended, _ := q.Settled(ctx, "s1", nil); ended != nil {
		t.Fatalf("expected nil when unshown, got %+v", ended)
	}
	// Now settle with the delivery id → ends it.
	ended, err := q.Settled(ctx, "s1", []int64{d.ID})
	if err != nil {
		t.Fatalf("settled err: %v", err)
	}
	if ended == nil || ended.State != Done {
		t.Fatalf("expected Done delivery, got %+v", ended)
	}
	if got, _ := q.Message(ctx, m.ID); got.State != Unanswered {
		t.Fatalf("message state %s, want %s", got.State, Unanswered)
	}
}

// TestReplyImpliesShownThenSettledEnds verifies that replying to a message in a
// delivery marks it shown, so a subsequent Settled (with no shownIDs) ends it.
func TestReplyImpliesShownThenSettledEnds(t *testing.T) {
	q, _ := newQueue(t)
	th := thread(t, q, "s1")
	m := post(t, q, th, "one", false)
	q.Next(ctx, "s1")

	// Reply to the message → delivery is now implicitly shown.
	if _, _, err := q.Reply(ctx, "s1", []int64{m.ID}, Working, ""); err != nil {
		t.Fatalf("reply err: %v", err)
	}
	// Settle without passing shown ids → still ends it (reply implied shown).
	ended, err := q.Settled(ctx, "s1", nil)
	if err != nil {
		t.Fatalf("settled err: %v", err)
	}
	if ended == nil || ended.State != Done {
		t.Fatalf("expected Done, got %+v", ended)
	}
}

// TestSettledIgnoresOtherSessionsShownIDs verifies that passing a delivery id
// belonging to another session does not affect that session's delivery.
func TestSettledIgnoresOtherSessionsShownIDs(t *testing.T) {
	q, _ := newQueue(t)
	q.Touch(ctx, Session{ID: "s2"})
	th1 := thread(t, q, "s1")
	th2 := thread(t, q, "s2")
	_ = post(t, q, th1, "for s1", false)
	_ = post(t, q, th2, "for s2", false)

	d1, _ := q.Next(ctx, "s1")
	d2, _ := q.Next(ctx, "s2")

	// Settle s1's turn, passing s2's delivery id as shown → s2's delivery must not be affected.
	ended, err := q.Settled(ctx, "s1", []int64{d2.ID})
	if err != nil {
		t.Fatalf("settled err: %v", err)
	}
	if ended != nil {
		t.Fatalf("s2's delivery id must not end s1's delivery (s1's was not shown): %+v", ended)
	}
	// s2's delivery is still inflight and unmodified.
	s2d, _ := q.Delivery(ctx, d2.ID)
	if s2d.State != InFlight || !s2d.ShownAt.IsZero() {
		t.Fatalf("s2 delivery affected: state=%s shownAt=%v", s2d.State, s2d.ShownAt)
	}
	// s1's delivery is still inflight.
	if d, _ := q.Inflight(ctx, "s1"); d == nil || d.ID != d1.ID {
		t.Fatalf("s1 delivery not inflight: %v", d)
	}
}

func TestReplyRejectsOtherSessionsAndBadStates(t *testing.T) {
	q, _ := newQueue(t)
	q.Touch(ctx, Session{ID: "s2"})
	th := thread(t, q, "s1")
	m := post(t, q, th, "hi", false)
	if _, _, err := q.Reply(ctx, "s1", []int64{m.ID}, Answered, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reply to undelivered: %v", err)
	}
	q.Next(ctx, "s1")
	if _, _, err := q.Reply(ctx, "s2", []int64{m.ID}, Answered, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other session: %v", err)
	}
	if _, _, err := q.Reply(ctx, "s1", []int64{m.ID}, "done", ""); err == nil {
		t.Fatal("bad state accepted")
	}
}

func TestLateReplyToUnanswered(t *testing.T) {
	q, _ := newQueue(t)
	th := thread(t, q, "s1")
	m1 := post(t, q, th, "one", false)
	m2 := post(t, q, th, "two", false)
	d, _ := q.Next(ctx, "s1")

	// End the turn: both messages become unanswered (pass the delivery id so it is settled).
	if _, err := q.Settled(ctx, "s1", []int64{d.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Message(ctx, m1.ID); got.State != Unanswered {
		t.Fatalf("m1 pre-state %s", got.State)
	}

	// Late reply with final state to m1: should be accepted.
	touched, skipped, err := q.Reply(ctx, "s1", []int64{m1.ID}, Answered, "late text")
	if err != nil {
		t.Fatal(err)
	}
	if len(touched) != 1 || touched[0] != m1.ID {
		t.Fatalf("touched %v, want [%d]", touched, m1.ID)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v, want none", skipped)
	}
	if got, _ := q.Message(ctx, m1.ID); got.State != Answered {
		t.Fatalf("m1 state after late reply: %s", got.State)
	}

	// Reply text must be recorded in the thread.
	all, _ := q.Messages(ctx, th.ID)
	var replyMsg *Message
	for i := range all {
		if all[i].State == AgentReply {
			replyMsg = &all[i]
		}
	}
	if replyMsg == nil || replyMsg.Body != "late text" || replyMsg.ReplyTo != m1.ID {
		t.Fatalf("reply message %+v in %+v", replyMsg, all)
	}

	// Delivery must still be done; a late reply must not reopen it.
	del, _ := q.Delivery(ctx, d.ID)
	if del.State != Done {
		t.Fatalf("delivery state after late reply: %s", del.State)
	}

	// Non-final state on an unanswered message: skipped.
	touched2, skipped2, err := q.Reply(ctx, "s1", []int64{m2.ID}, Working, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(touched2) != 0 {
		t.Fatalf("touched2 %v, want none", touched2)
	}
	if len(skipped2) != 1 || skipped2[0].ID != m2.ID || skipped2[0].State != Unanswered {
		t.Fatalf("skipped2 %+v", skipped2)
	}
	if got, _ := q.Message(ctx, m2.ID); got.State != Unanswered {
		t.Fatalf("m2 state changed unexpectedly: %s", got.State)
	}

	// Reply to already-answered m1: skipped, but text still recorded.
	touched3, skipped3, err := q.Reply(ctx, "s1", []int64{m1.ID}, Answered, "second late text")
	if err != nil {
		t.Fatal(err)
	}
	if len(touched3) != 0 {
		t.Fatalf("touched3 %v, want none", touched3)
	}
	if len(skipped3) != 1 || skipped3[0].ID != m1.ID || skipped3[0].State != Answered {
		t.Fatalf("skipped3 %+v", skipped3)
	}
	// Text should still be recorded (first valid id's thread).
	all2, _ := q.Messages(ctx, th.ID)
	var replies []Message
	for _, msg := range all2 {
		if msg.State == AgentReply {
			replies = append(replies, msg)
		}
	}
	if len(replies) != 2 || replies[1].Body != "second late text" {
		t.Fatalf("replies %+v", replies)
	}
}

func TestStuckReleaseMoveInterrupt(t *testing.T) {
	q, c := newQueue(t)
	q.Touch(ctx, Session{ID: "s2"})
	th := thread(t, q, "s1")
	m := post(t, q, th, "hello", false)
	d, _ := q.Next(ctx, "s1")
	c.add(StuckAfter + time.Second)
	if got, _ := q.Delivery(ctx, d.ID); !got.Stuck {
		t.Fatal("not stuck")
	}
	if err := q.MoveDelivery(ctx, d.ID, "s2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Delivery(ctx, d.ID); got.State != Moved {
		t.Fatalf("state %s", got.State)
	}
	d2, _ := q.Next(ctx, "s2")
	if d2 == nil || d2.Messages[0].ID != m.ID {
		t.Fatalf("moved delivery %+v", d2)
	}
	if err := q.Release(ctx, d2.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Message(ctx, m.ID); got.State != Unanswered {
		t.Fatalf("after release %s", got.State)
	}
	// Interrupt: a delivery left in flight by a dead serve.
	m2 := post(t, q, th, "again", false) // thread moved to s2 with the delivery
	d3, _ := q.Next(ctx, "s2")
	if d3 == nil {
		t.Fatal("no delivery")
	}
	if n, _ := q.Interrupt(ctx); n != 1 {
		t.Fatalf("interrupted %d", n)
	}
	if got, _ := q.Message(ctx, m2.ID); got.State != Interrupted {
		t.Fatalf("m2 %s", got.State)
	}
	sessions, _ := q.Sessions(ctx)
	for _, s := range sessions {
		if s.Busy {
			t.Errorf("%s still busy", s.ID)
		}
	}
}

// TestSettledRefreshesTouchedAtWhenShown verifies that when Settled records
// shown_at for a delivery it also refreshes touched_at to the shown time.
// Without the fix, touched_at would remain at sent_at (t0) while now is
// t0+15min, making the delivery appear Stuck (touched_at > StuckAfter ago);
// with the fix, touched_at is refreshed to now so Stuck is false.
// Fail-before evidence: without `touched_at = ?` in the shown_at UPDATE,
// ended.TouchedAt equals the sent time (t0), not the shown time (t0+15min).
func TestSettledRefreshesTouchedAtWhenShown(t *testing.T) {
	q, c := newQueue(t)
	th := thread(t, q, "s1")
	_ = post(t, q, th, "one", false)
	d, err := q.Next(ctx, "s1")
	if err != nil || d == nil {
		t.Fatalf("Next: %v %v", d, err)
	}
	sentAt := d.TouchedAt

	// Advance clock past StuckAfter; the delivery would be Stuck.
	c.add(StuckAfter + 5*time.Minute)
	// Confirm it looks stuck at this point.
	if got, _ := q.Delivery(ctx, d.ID); !got.Stuck {
		t.Fatal("delivery should be Stuck before being shown")
	}

	// Settled with the delivery's id marks it shown (and ends it).
	// touched_at must be refreshed to now (the shown time), not left at sent_at.
	now := q.Now()
	ended, err := q.Settled(ctx, "s1", []int64{d.ID})
	if err != nil || ended == nil {
		t.Fatalf("Settled: %v %v", ended, err)
	}

	// touched_at must equal the shown time, not the sent time.
	if ended.TouchedAt.Equal(sentAt) {
		t.Fatalf("touched_at was not refreshed: still at sent time %v (want ~%v)", ended.TouchedAt, now)
	}
	if ended.TouchedAt.Before(now.Add(-time.Second)) || ended.TouchedAt.After(now.Add(time.Second)) {
		t.Fatalf("touched_at %v not close to shown time %v", ended.TouchedAt, now)
	}
	// shown_at should equal touched_at (both set to now in the same UPDATE).
	if !ended.ShownAt.Equal(ended.TouchedAt) {
		t.Fatalf("shown_at %v != touched_at %v", ended.ShownAt, ended.TouchedAt)
	}
	// Delivery is Done, so Stuck is false (the state check in Delivery()).
	if ended.Stuck {
		t.Fatal("ended delivery must not be Stuck")
	}
}

func TestRenderGolden(t *testing.T) {
	q, c := newQueue(t)
	th := thread(t, q, "s1")
	first := post(t, q, th, "propose decisions for the 40 chime PRs", false)
	q.Next(ctx, "s1")
	c.add(time.Minute)
	m1, _ := q.Post(ctx, th.ID, "Merge these if CI is green.", Attached{Keys: []string{
		"pr:recreational-spreadsheeting/bettor-help-platform#670", "pr:recreational-spreadsheeting/bettor-help-platform#671",
		"pr:recreational-spreadsheeting/bettor-help-platform#672", "pr:recreational-spreadsheeting/bettor-help-platform#673"}}, false)
	c.add(3 * time.Minute)
	q.Post(ctx, th.ID, "Make this rule skip anything touching packages/infra.", Attached{Rule: "dependabot-minor"}, true)
	q.Post(ctx, th.ID, "Actually skip #673, it's a major bump.", Attached{}, true)
	bid, _, _ := q.DraftBatch(ctx, th.ID)
	c.add(2 * time.Minute)
	q.SendBatch(ctx, bid)
	q.Reply(ctx, "s1", []int64{first.ID}, Answered, "done")
	d, _ := q.Next(ctx, "s1")
	prev, _ := q.Previous(ctx, "s1", d.ID)
	got := Render(*d, prev.Messages[0].Body, "Court accepted 31 of your 40 proposals and changed 9 to keep.", time.UTC)
	golden := filepath.Join("testdata", "delivery.golden")
	if *update {
		os.WriteFile(golden, []byte(got), 0o644)
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run go test -run Golden -update)", err)
	}
	if got != string(want) {
		t.Fatalf("render mismatch:\n--- got\n%s\n--- want\n%s", got, want)
	}
	_ = m1
}
