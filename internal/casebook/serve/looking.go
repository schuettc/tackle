package serve

import (
	"context"
	"strings"

	"github.com/schuettc/tackle/internal/casebook/deliver"
)

// LookIntoText is what "ask ‹session› to look into it" sends the page's
// session about key, with key attached (the page writes the same text:
// web/attention.ts lookIntoText). A message that starts with it, with key
// attached, is how serve knows a session is looking into the item.
func LookIntoText(key string) string {
	return lookIntoPrefix(key) + " check its CI, recent activity and anything blocking it. If a check is failing, find the cause and what would fix it. Add what you find as evidence (casebook_evidence) and recommend what to do with a one-line reason (casebook_propose)."
}

func lookIntoPrefix(key string) string { return "Look into " + key + ":" }

// Looking says a session is looking into an item: Court asked it to (the
// message), and the message is queued or being worked on, not yet
// answered, declined or failed.
type Looking struct {
	Message int64           `json:"message"`
	Session deliver.Session `json:"session"`
}

// lookingInto maps each item a session is looking into to who: the newest
// such ask per item. Errors read as nobody looking (the marker is a hint).
func (s *Server) lookingInto(ctx context.Context) map[string]*Looking {
	msgs, err := s.Queue.Underway(ctx)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	sessions := map[string]deliver.Session{}
	out := map[string]*Looking{}
	for _, m := range msgs {
		for _, k := range m.Attached.Keys {
			if !strings.HasPrefix(m.Body, lookIntoPrefix(k)) {
				continue
			}
			sess, ok := sessions[m.Session]
			if !ok {
				sess, err = s.Queue.Session(ctx, m.Session)
				if err != nil {
					sess = deliver.Session{ID: m.Session}
				}
				sessions[m.Session] = sess
			}
			out[k] = &Looking{Message: m.ID, Session: sess}
		}
	}
	return out
}

// withLooking sets each item's Looking from looking.
func withLooking(items []ItemView, looking map[string]*Looking) {
	for i := range items {
		items[i].Looking = looking[items[i].ID]
	}
}
