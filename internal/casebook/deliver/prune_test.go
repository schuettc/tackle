package deliver

import (
	"testing"
	"time"
)

// Prune never deletes a session anything refers to. Every place the working
// database holds a session id (db.go's schema):
//
//   - threads.session_id, deliveries.session_id, progress.session_id,
//     progress_log.session_id: the session id itself;
//   - messages.author: an agent's reply or worked message, which stays in a
//     thread after the thread moves to another session;
//   - jobs.session: a plan approved for the session (a later "hand to agent"
//     makes a thread with it);
//   - proposals.source, evidence.author: "<harness>:<id>";
//   - events.payload: the live log's JSON (sessions, proposals, delivery
//     events name the session);
//
// and, outside the database, a rule draft's created_by ("<harness>:<id>"),
// which serve passes in as spare. batches and needs_you hold no session id:
// they reach one only through a thread or a job (covered above).
func TestPruneSparesASessionAnythingReferences(t *testing.T) {
	const keep = 7 * 24 * time.Hour
	exec := func(t *testing.T, q *Queue, query string, args ...any) {
		t.Helper()
		if _, err := q.DB.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		refer func(t *testing.T, q *Queue) // makes one reference to "old"
		spare []string
	}{
		{"nothing (control: pruned)", nil, nil},
		{"a thread", func(t *testing.T, q *Queue) { thread(t, q, "old") }, nil},
		{"a delivery", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO deliveries(session_id, state, sent_at, touched_at) VALUES ('old', 'done', 1, 1)`)
		}, nil},
		{"a progress line", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO progress(session_id, text, started_at, updated_at) VALUES ('old', 'x', 1, 1)`)
		}, nil},
		{"a progress log row", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO progress_log(session_id, text, at) VALUES ('old', 'x', 1)`)
		}, nil},
		{"a reply it wrote, in a thread now another session's", func(t *testing.T, q *Queue) {
			th := thread(t, q, "s1")
			exec(t, q, `INSERT INTO messages(thread_id, author, body, state, created_at) VALUES (?, 'old', 'done', 'reply', 1)`, th.ID)
		}, nil},
		{"a job approved for it", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO jobs(plan_json, machine, session, state, created_at) VALUES ('{}', 'm', 'old', 'approved', 1)`)
		}, nil},
		{"a job approved for it, waiting on Court (needs_you)", func(t *testing.T, q *Queue) {
			res, err := q.DB.ExecContext(ctx, `INSERT INTO jobs(plan_json, machine, session, state, created_at) VALUES ('{}', 'm', 'old', 'running', 1)`)
			if err != nil {
				t.Fatal(err)
			}
			job, _ := res.LastInsertId()
			exec(t, q, `INSERT INTO needs_you(job_id, kind, created_at) VALUES (?, 'ask', 1)`, job)
		}, nil},
		{"a proposal it made", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO proposals(key, disposition, source, created_at) VALUES ('pr:o/r#1', 'keep', 'pi:old', 1)`)
		}, nil},
		{"a proposal it made with no harness", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO proposals(key, disposition, source, created_at) VALUES ('pr:o/r#1', 'keep', 'agent:old', 1)`)
		}, nil},
		{"evidence it added", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO evidence(key, text, author, created_at) VALUES ('pr:o/r#1', 'seen', 'pi:old', 1)`)
		}, nil},
		{"an event naming it", func(t *testing.T, q *Queue) {
			exec(t, q, `INSERT INTO events(kind, payload, created_at) VALUES ('proposals', '{"source":"pi:old"}', 1)`)
		}, nil},
		{"a rule draft it wrote (serve's spare list)", nil, []string{"old"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q, clk := newQueue(t)
			if err := q.Touch(ctx, Session{ID: "old", Harness: "pi", CWD: "/o", PID: 2}); err != nil {
				t.Fatal(err)
			}
			// A session whose id merely contains "old" is not a reference.
			if err := q.Touch(ctx, Session{ID: "older", Harness: "pi", CWD: "/o", PID: 3}); err != nil {
				t.Fatal(err)
			}
			if c.refer != nil {
				c.refer(t, q)
			}
			clk.add(keep + time.Hour)
			if err := q.Touch(ctx, Session{ID: "s1", Harness: "pi", CWD: "/w", PID: 42}); err != nil {
				t.Fatal(err)
			}
			if _, err := q.Prune(ctx, keep, c.spare...); err != nil {
				t.Fatal(err)
			}
			_, err := q.Session(ctx, "old")
			kept := err == nil
			if want := c.refer != nil || c.spare != nil; kept != want {
				t.Errorf("old kept = %v, want %v (%v)", kept, want, err)
			}
		})
	}
}
