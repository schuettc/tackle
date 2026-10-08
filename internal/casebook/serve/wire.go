package serve

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/schuettc/tackle/internal/casebook/apply"
	"github.com/schuettc/tackle/internal/casebook/deliver"
	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/journal"
	"github.com/schuettc/tackle/internal/casebook/propose"
	"github.com/schuettc/tackle/internal/casebook/rules"
	"github.com/schuettc/tackle/internal/casebook/store"
)

// SummaryView is the response body of GET /api/summary.
type SummaryView struct {
	Machine  string    `json:"machine"`
	User     string    `json:"user"`
	Head     string    `json:"head"`
	BuiltAt  time.Time `json:"built_at"`
	SyncedAt time.Time `json:"synced_at"`
	// OfflineQueued counts local commits the casebook remote doesn't have
	// yet (the page says "offline · N queued"). While serve's background
	// push is in flight it is 0, unless the push before it failed: a
	// decision on its way out isn't offline.
	OfflineQueued int `json:"offline_queued"`
	// PushError is why serve's last push failed, when it failed for a
	// reason other than the network (a sync conflict it can't resolve, the
	// remote refusing), in serve's words; the page says "push failed · N
	// queued" and shows it. Cleared by the next push that succeeds, and
	// whenever nothing is queued. Empty while pushes merely can't reach the
	// remote ("offline · N queued").
	PushError string         `json:"push_error,omitempty"`
	Counts    map[string]int `json:"counts"`
	Notices   []string       `json:"notices"`
	Sessions  int            `json:"sessions"`
	// SyncIntervalMS is one sync interval: a plan refuses an index built
	// longer ago than this (built_at), so the page can say how old the
	// observations are.
	SyncIntervalMS int64 `json:"sync_interval_ms"`
	// Syncing: a sync POST /api/sync started is running.
	Syncing bool `json:"syncing"`
	// Recommended and NotRecommended count the items that need a decision
	// (the waiting, due and new views, each item once): those with a
	// pending proposal, and those casebook_next still has to hand out.
	Recommended    int `json:"recommended"`
	NotRecommended int `json:"not_recommended"`
	// Cursor is the live event log's head when serve began this answer:
	// the page starts its live stream there (?since=), so it hears what
	// changes after its first reads and never replays the log.
	Cursor int64 `json:"cursor"`
}

// ItemView is an item as the page and the agent see it. Its url (engine.Item's
// URL) is serve's, built from the key by githubURL: the item's page on GitHub,
// "" for a branch or a worktree, which have none.
type ItemView struct {
	engine.Item
	Proposal *propose.Proposal `json:"proposal,omitempty"`
	// Looking: a session is looking into the item ("ask ‹session› to look
	// into it", not yet settled); nil when none is.
	Looking *Looking `json:"looking,omitempty"`
}

// newItemView is it as the page and the agent see it, with its proposal
// (nil for none) and the GitHub page its key names.
func newItemView(it engine.Item, p *propose.Proposal) ItemView {
	it.URL = githubURL(it.Key)
	return ItemView{Item: it, Proposal: p}
}

// githubURL is the GitHub page k names: a pull request's, an issue's or a
// repository's; "" for a branch or a worktree.
func githubURL(k item.Key) string {
	switch k.Kind {
	case item.KindPR:
		return fmt.Sprintf("https://github.com/%s/pull/%d", k.Repo(), k.Number)
	case item.KindIssue:
		return fmt.Sprintf("https://github.com/%s/issues/%d", k.Repo(), k.Number)
	case item.KindRepo:
		return "https://github.com/" + k.Repo()
	}
	return ""
}

// ItemsView is the response body of GET /api/items.
type ItemsView struct {
	Total int        `json:"total"`
	Items []ItemView `json:"items"`
	// LeftOpen is an Attention view's left-open group: the kept items still
	// open that the view would list, in key order, the first page of them
	// (up to the request's limit). They need no decision: Total and the
	// view's count leave them out; LeftOpenTotal counts them.
	LeftOpen      []ItemView `json:"left_open"`
	LeftOpenTotal int        `json:"left_open_total"`
}

// ItemDetailView is the response body of GET /api/item.
type ItemDetailView struct {
	Item      ItemView           `json:"item"`
	Proposals []propose.Proposal `json:"proposals"`
	Evidence  []propose.Evidence `json:"evidence"`
	History   []journal.Event    `json:"history"`
	Decisions []store.LogEntry   `json:"decisions"`
}

// DecideResult is the response body of POST /api/decide and POST
// /api/proposals/change. serve answers once the decisions are committed
// locally, their proposals retired and the index rebuilt; the push to the
// casebook remote follows in the background (the "push" live event, PushEvent).
type DecideResult struct {
	Decided     int      `json:"decided"`
	DecidedKeys []string `json:"decided_keys"`
	Errors      []string `json:"errors"`
	// Decisions is, per decided key, the decision this request committed:
	// what an undo of it expects to find (POST /api/decisions/undo).
	Decisions map[string]Committed `json:"decisions"`
}

// AcceptResult is the response body of POST /api/proposals/accept. Like
// DecideResult, it doesn't wait for the push.
type AcceptResult struct {
	Accepted int      `json:"accepted"`
	Errors   []string `json:"errors"`
	// Decisions is, per decided key, the decision this request committed.
	Decisions map[string]Committed `json:"decisions"`
}

// Committed is a decision as a request committed it, decided_at to the
// second: what POST /api/decisions/undo compares the item's decision with.
type Committed struct {
	Disposition string    `json:"disposition"`
	Until       string    `json:"until"`
	Note        string    `json:"note"`
	DecidedAt   time.Time `json:"decided_at"`
}

// DecisionUndoResult is the response body of POST /api/decisions/undo:
// Decision is the decision the undo restored, null when it removed one.
// Like DecideResult it doesn't wait for the push.
type DecisionUndoResult struct {
	Undone   bool       `json:"undone"`
	Decision *Committed `json:"decision"`
}

// Push states, as the "push" live event says them.
const (
	PushDone    = "done"    // the remote has every local commit serve pushed
	PushOffline = "offline" // the remote couldn't be reached: the commits stay queued
	PushFailed  = "failed"  // the push failed otherwise (Error says why): the commits stay queued
)

// PushEvent is the payload of the "push" live event: serve's background push
// of decisions to the casebook remote ended (one event per push; a push that
// coalesced decides made during the one before it is one more). The page
// asks GET /api/summary again for offline_queued.
type PushEvent struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
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

// DeliveryView is the response body of GET /api/session/delivery.
// Delivery is null when there is no in-flight delivery for the session.
type DeliveryView struct {
	Delivery *deliver.Delivery `json:"delivery"`
}

// WorkedView is the progress history from a completed turn, carried on a
// 'worked' message. It matches deliver.WorkedView.
type WorkedView = deliver.WorkedView

// ProgressLine is one entry in a turn's progress history. It matches
// propose.ProgressLine and is exported on WorkedView.Lines.
type ProgressLine = propose.ProgressLine

// MessageView wraps deliver.Message and adds the decoded WorkedView for
// messages with state "worked" (a turn's progress fold-in, spec §3.5).
type MessageView struct {
	deliver.Message
	Worked *WorkedView `json:"worked,omitempty"`
}

// toMessageView converts a deliver.Message to a MessageView, decoding the
// worked field when present.
func toMessageView(m deliver.Message) MessageView {
	mv := MessageView{Message: m}
	if m.WorkedJSON != "" {
		var w WorkedView
		if err := json.Unmarshal([]byte(m.WorkedJSON), &w); err == nil {
			mv.Worked = &w
		}
	}
	return mv
}

// MessagesView is the response body of GET /api/messages.
type MessagesView struct {
	Messages []MessageView     `json:"messages"`
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

// InterruptedView is the response body of GET /api/agent/interrupted: what
// this serve's start interrupted of one session's deliveries (empty lists:
// nothing). StartedAt is this serve's (the advert's), so a channel can tell
// the answer is about the restart it noticed.
type InterruptedView struct {
	StartedAt  time.Time `json:"started_at"`
	Deliveries []int64   `json:"deliveries"`
	Messages   []int64   `json:"messages"`
}

// OpenResult is the response body of POST /api/agent/open.
type OpenResult struct {
	Opened string `json:"opened"`
	// Session is the session the page opened attached to ("" for none).
	Session string `json:"session,omitempty"`
}

// SettledResult is the response body of POST /api/agent/settled.
type SettledResult struct {
	Session  string `json:"session"`
	WorkedMs int64  `json:"worked_ms"`
	Delivery *int64 `json:"delivery,omitempty"`
}

// RulesView is the response body of GET /api/rules.
type RulesView struct {
	Rules []RuleRow `json:"rules"`
}

// RuleRow is one row in the rules list: the rule and its track record.
type RuleRow struct {
	Rule   rules.Rule        `json:"rule"`
	Record rules.TrackRecord `json:"record"`
	// Invalid is why the rule is not valid (serve's validation message,
	// naming the condition), or why its file can't be read; "" when valid.
	Invalid string `json:"invalid,omitempty"`
	// Matches is how many items the rule matches now (its exclusions left
	// out); Excluded is how many it excludes. Matches is 0 while its
	// conditions are invalid.
	Matches  int `json:"matches"`
	Excluded int `json:"excluded"`
}

// RuleDetailView is the response body of GET /api/rule, POST /api/rules/draft,
// POST /api/rules/activate, POST /api/rules/deactivate, POST /api/rules/exclude,
// POST /api/rules/include, and POST /api/agent/rule-draft.
type RuleDetailView struct {
	Rule    rules.Rule        `json:"rule"`
	Record  rules.TrackRecord `json:"record"`
	Matches MatchPreview      `json:"matches"`
	// Version names this exact content of the rule (rules.Version). The page
	// sends it back with activate and save; serve refuses (409) a stale one.
	Version string `json:"version"`
	// Invalid is why the rule is not valid (as RuleRow.Invalid). An invalid
	// rule's conditions are not previewed: Matches is empty.
	Invalid string `json:"invalid,omitempty"`
}

// MatchPreview is the paginated, grouped match list returned by
// POST /api/rules/preview and embedded in RuleDetailView.
type MatchPreview struct {
	Total    int           `json:"total"`
	ByReason []ReasonCount `json:"by_reason"`
	Groups   []RepoGroup   `json:"groups"`
	Page     []MatchRow    `json:"page"`
	// Dispositions is what the rule may propose: those valid for every kind
	// of item its conditions can match (rules.Dispositions).
	Dispositions []string `json:"dispositions"`
}

// ReasonCount is one reason bucket inside a MatchPreview.
type ReasonCount struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

// RepoGroup is one repo bucket inside a MatchPreview.
type RepoGroup struct {
	Repo    string        `json:"repo"`
	Count   int           `json:"count"`
	Reasons []ReasonCount `json:"reasons"`
}

// MatchRow is one row in a MatchPreview page.
type MatchRow struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
	Repo   string `json:"repo"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// VocabularyView is the response body of GET /api/rules/vocabulary.
type VocabularyView struct {
	Fields []rules.Field `json:"fields"`
	// NoteTokens are the placeholders a rule's note may use.
	NoteTokens []rules.NoteToken `json:"note_tokens"`
}

// DecisionVocabView is the response body of GET /api/decisions/vocabulary.
// It is generated from the Go item package and is the single source of truth
// for which dispositions are valid per kind and which ones require an until
// condition.  The page deletes its hand-copied tables and reads this instead.
//
// It also carries all the decide wording (item.Question, item.Choices,
// item.NotNowForms): the page writes no question, label or sentence of its
// own.
type DecisionVocabView struct {
	Kinds      []KindVocab `json:"kinds"`
	UntilForms []UntilForm `json:"until_forms"`
	// NotNow is Not now's conditions, in the order the page offers them.
	NotNow []NotNowForm `json:"not_now"`
	// LookInto is "Look into it": the card's label and sentence, and the
	// messages it and "look into all N" send.
	LookInto LookIntoVocab `json:"look_into"`
}

// KindVocab describes the valid decisions for one item kind. Allowed and
// NeedsUntil are what the server accepts (watch included); Question and
// Choices are what the decide step offers (watch never: Not now writes
// wait).
type KindVocab struct {
	Kind       string        `json:"kind"`
	Allowed    []string      `json:"allowed"`
	NeedsUntil []string      `json:"needs_until"`
	Question   string        `json:"question"`
	Choices    []ChoiceVocab `json:"choices"`
}

// ChoiceVocab is one answer card: its disposition, label and what choosing
// it does (Says). Outward choices only go to To apply. NeedsUntil choices
// (Not now) need a condition from DecisionVocabView.NotNow.
type ChoiceVocab struct {
	Disposition string `json:"disposition"`
	Label       string `json:"label"`
	Says        string `json:"says"`
	Outward     bool   `json:"outward"`
	NeedsUntil  bool   `json:"needs_until"`
}

// NotNowForm is one Not now condition. Template is an until form with one
// %s hole filled with what Asks names: "days" (the page fills the date
// today + Days), "date", "pr", "pr-or-issue" or "repo" (the page asks for
// it). When Asks is "" the form is fixed and Template is the whole until.
type NotNowForm struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Template string `json:"template"`
	Asks     string `json:"asks"`
	Days     int    `json:"days,omitempty"`
}

// NextView is the response body of GET /api/agent/next (casebook_next):
// the next item that needs a recommendation, with the choices its kind
// offers, Not now's conditions, the guide and how to answer. Done is true,
// and Item nil, when every item that needs a decision has a pending
// proposal. Left counts those still without one, this item included.
type NextView struct {
	Done       bool            `json:"done"`
	Left       int             `json:"left"`
	Item       *ItemDetailView `json:"item"`
	Choices    []ChoiceVocab   `json:"choices"`
	NotNow     []NotNowForm    `json:"not_now"`
	Guide      string          `json:"guide"`
	ProposeHow string          `json:"propose_how"`
}

// ClearResult is the response body of POST /api/decisions/clear. Cleared is
// false when the item had no decision. Like DecideResult it doesn't wait for
// the push (PushedLater is always true).
type ClearResult struct {
	Cleared     bool `json:"cleared"`
	PushedLater bool `json:"pushed_later"`
}

// UntilForm is one until operator with its syntax pattern and one example
// value, generated from item.UntilForms().
type UntilForm struct {
	Op      string `json:"op"`
	Syntax  string `json:"syntax"`
	Example string `json:"example"`
}

// JobStepResult is the response body of POST /api/agent/job-step.
type JobStepResult struct {
	JobID  int64  `json:"job_id"`
	StepID int64  `json:"step_id"`
	State  string `json:"state"`
}

// JobAskResult is the response body of POST /api/agent/job-ask.
type JobAskResult struct {
	NeedsYou apply.NeedsYou `json:"needs_you"`
}

// PlanView is the response body of POST /api/apply/plan.
type PlanView struct {
	Plan   apply.Plan    `json:"plan"`
	Groups []apply.Group `json:"groups"`
	Job    apply.Job     `json:"job"`
}

// JobView is the response body of GET /api/job and POST /api/apply/approve.
type JobView struct {
	Job      apply.Job        `json:"job"`
	NeedsYou []apply.NeedsYou `json:"needs_you"`
}

// JobsView is the response body of GET /api/jobs.
type JobsView struct {
	Jobs []apply.Job `json:"jobs"`
}

// NeedsYouView is the response body of GET /api/needs-you.
type NeedsYouView struct {
	Cards []apply.NeedsYou `json:"cards"`
}

// SessionProgressView is the response body of GET /api/session/progress.
// Progress is nil when the session has no live progress line. Now is serve's
// clock as it answered: the page ages the line by Now − UpdatedAt, both
// serve's, so a page whose clock differs from serve's still reads its age.
type SessionProgressView struct {
	Progress *propose.Progress `json:"progress"`
	Now      time.Time         `json:"now"`
}

// AnswerResult is the response body of POST /api/jobs/answer.
type AnswerResult struct {
	NeedsYou apply.NeedsYou `json:"needs_you"`
}

// UndoResult is the response body of POST /api/jobs/undo.
type UndoResult struct {
	StepID int64 `json:"step_id"`
	Sent   bool  `json:"sent"` // true if sent to agent, false if ran locally
}
