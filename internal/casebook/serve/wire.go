package serve

import (
	"time"

	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/journal"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/store"
)

// SummaryView is the response body of GET /api/summary.
type SummaryView struct {
	Machine       string         `json:"machine"`
	User          string         `json:"user"`
	Head          string         `json:"head"`
	BuiltAt       time.Time      `json:"built_at"`
	SyncedAt      time.Time      `json:"synced_at"`
	OfflineQueued int            `json:"offline_queued"`
	Counts        map[string]int `json:"counts"`
	Notices       []string       `json:"notices"`
	Sessions      int            `json:"sessions"`
}

// ItemsView is the response body of GET /api/items.
type ItemsView struct {
	Total int        `json:"total"`
	Items []ItemView `json:"items"`
}

// ItemDetailView is the response body of GET /api/item.
type ItemDetailView struct {
	Item      ItemView           `json:"item"`
	Proposals []propose.Proposal `json:"proposals"`
	Evidence  []propose.Evidence `json:"evidence"`
	History   []journal.Event    `json:"history"`
	Decisions []store.LogEntry   `json:"decisions"`
}

// DecideResult is the response body of POST /api/decide and POST /api/proposals/change.
type DecideResult struct {
	Decided int      `json:"decided"`
	Errors  []string `json:"errors"`
	Pushed  bool     `json:"pushed"`
}

// AcceptResult is the response body of POST /api/proposals/accept.
type AcceptResult struct {
	Accepted int      `json:"accepted"`
	Errors   []string `json:"errors"`
	Pushed   bool     `json:"pushed"`
}

// RejectResult is the response body of POST /api/proposals/reject.
type RejectResult struct {
	Rejected int `json:"rejected"`
}

// SessionsView is the response body of GET /api/sessions.
type SessionsView struct {
	Sessions []deliver.Session `json:"sessions"`
}

// ThreadsView is the response body of GET /api/threads.
type ThreadsView struct {
	Threads []deliver.Thread `json:"threads"`
}

// MessagesView is the response body of GET /api/messages.
type MessagesView struct {
	Messages []deliver.Message `json:"messages"`
	Batch    int64             `json:"batch"`
	Drafts   []deliver.Message `json:"drafts"`
}

// SendBatchResult is the response body of POST /api/batches/send.
type SendBatchResult struct {
	Sent int `json:"sent"`
}

// ResendResult is the response body of POST /api/messages/resend.
type ResendResult struct {
	Resent int `json:"resent"`
}

// WaitView is the response body of GET /api/agent/wait (when a delivery is ready).
type WaitView struct {
	Delivery *deliver.Delivery `json:"delivery"`
	Text     string            `json:"text"`
}

// ReplyResult is the response body of POST /api/agent/reply.
type ReplyResult struct {
	Settled []int64                  `json:"settled"`
	Skipped []deliver.SkippedMessage `json:"skipped"`
}

// ProposeResult is the response body of POST /api/agent/propose.
type ProposeResult struct {
	Proposed  int                `json:"proposed"`
	Proposals []propose.Proposal `json:"proposals"`
	Errors    []string           `json:"errors"`
}

// StatusView is the response body of GET /api/agent/status.
type StatusView struct {
	Counts   map[string]int `json:"counts"`
	Since    string         `json:"since"`
	PageOpen bool           `json:"page_open"`
}

// OpenResult is the response body of POST /api/agent/open.
type OpenResult struct {
	Opened string `json:"opened"`
}

// SettledResult is the response body of POST /api/agent/settled.
type SettledResult struct {
	Session  string `json:"session"`
	WorkedMs int64  `json:"worked_ms"`
	Delivery *int64 `json:"delivery,omitempty"`
}
