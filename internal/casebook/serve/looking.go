package serve

import (
	"context"
	"strings"

	"github.com/schuettc/tackle/internal/casebook/deliver"
)

// The words of "Look into it", serve's alone: the decision vocabulary
// hands them to the page (LookIntoVocab), which fills the holes in. The
// open item's last card is lookIntoLabel and lookIntoSays; it sends
// lookIntoMessage. The list foot's "look into all N" sends
// lookIntoMessageMany, one message for the group.
const (
	lookIntoLabel       = "Look into it"
	lookIntoSays        = "{session} checks its CI and recent activity, finds what's wrong, and comes back with a recommendation. Nothing is decided yet."
	lookIntoMessage     = "Look into {key}: check its CI, recent activity and anything blocking it. If a check is failing, find the cause and what would fix it. Add what you find as evidence (casebook_evidence) and recommend what to do with a one-line reason (casebook_propose)."
	lookIntoMessageMany = "Look into these items: {keys}. For each, check its CI, recent activity and anything blocking it; if a check is failing, find the cause and what would fix it. Add what you find as evidence (casebook_evidence) and recommend what to do with a one-line reason (casebook_propose)."
)

// PurposeLookInto is the purpose the page posts the ask with
// (POST /api/messages "purpose"). It, not the words, is how serve knows a
// session is looking into the item: a message Court types with the same
// words is just a message.
const PurposeLookInto = "look-into"

// LookIntoText is the ask's message about key.
func LookIntoText(key string) string {
	return strings.ReplaceAll(lookIntoMessage, "{key}", key)
}

// LookIntoManyText is the ask's message about several items, their keys
// joined with ", " (the page joins them the same way).
func LookIntoManyText(keys []string) string {
	return strings.ReplaceAll(lookIntoMessageMany, "{keys}", strings.Join(keys, ", "))
}

// LookIntoVocab is the ask's words in the decision vocabulary: the card's
// label and sentence ({session}: the session's name), the message for one
// item ({key}: its key) and for several ({keys}: their keys, joined with
// ", ").
type LookIntoVocab struct {
	Label       string `json:"label"`
	Says        string `json:"says"`
	Message     string `json:"message"`
	MessageMany string `json:"message_many"`
}

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
	msgs, err := s.Queue.Underway(ctx, PurposeLookInto)
	if err != nil || len(msgs) == 0 {
		return nil
	}
	sessions := map[string]deliver.Session{}
	out := map[string]*Looking{}
	for _, m := range msgs {
		for _, k := range m.Attached.Keys {
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
