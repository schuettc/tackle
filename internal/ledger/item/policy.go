package item

import (
	"bytes"
	"fmt"
	"time"

	"github.com/BurntSushi/toml"
)

// Policy holds the attention thresholds, in days (policy.toml in the ledger
// repo, shared by every machine).
type Policy struct {
	OutgoingPRStaleDays int `toml:"outgoing_pr_stale_days"`
	IncomingNoReplyDays int `toml:"incoming_no_reply_days"`
	UnpushedDays        int `toml:"unpushed_days"`
	RepoDormantDays     int `toml:"repo_dormant_days"`
}

// DefaultPolicy is what `ledger init` writes.
func DefaultPolicy() Policy {
	return Policy{OutgoingPRStaleDays: 14, IncomingNoReplyDays: 7, UnpushedDays: 3, RepoDormantDays: 365}
}

// DecodePolicy parses policy.toml over the defaults, rejecting unknown keys and
// non-positive thresholds.
func DecodePolicy(b []byte) (Policy, error) {
	p := DefaultPolicy()
	md, err := toml.Decode(string(b), &p)
	if err != nil {
		return p, fmt.Errorf("policy.toml: %w", err)
	}
	if und := md.Undecoded(); len(und) > 0 {
		return p, fmt.Errorf("policy.toml: unknown key %q", und[0].String())
	}
	for name, v := range map[string]int{
		"outgoing_pr_stale_days": p.OutgoingPRStaleDays, "incoming_no_reply_days": p.IncomingNoReplyDays,
		"unpushed_days": p.UnpushedDays, "repo_dormant_days": p.RepoDormantDays,
	} {
		if v <= 0 {
			return p, fmt.Errorf("policy.toml: %s must be > 0", name)
		}
	}
	return p, nil
}

// EncodePolicy renders p as policy.toml.
func EncodePolicy(p Policy) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("# Attention thresholds for the ledger, in days. Every machine reads this file.\n")
	err := toml.NewEncoder(&buf).Encode(p)
	return buf.Bytes(), err
}

// Hit is one policy flag on an item.
type Hit struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

// Signals are the per-item facts policies read.
type Signals struct {
	Kind           Kind
	Direction      string // pr/issue: outgoing, incoming or own
	Open           bool
	UpdatedAt      time.Time // pr/issue: last update; repo: pushed at
	CreatedAt      time.Time
	LastReplyByMe  time.Time // latest comment (or creation) by the user
	LastActivity   time.Time // latest comment (or creation) by anyone
	Archived       bool
	Undecided      bool
	OldestUnpushed time.Time // branch/worktree: oldest local-only commit
	UnpushedWhere  string    // "machine:path", for the detail
}

// Evaluate returns p's hits for an item. Ignored items get none.
func (p Policy) Evaluate(s Signals, ignored bool, now time.Time) []Hit {
	if ignored {
		return nil
	}
	days := func(t time.Time) int { return int(now.Sub(t).Hours() / 24) }
	var hits []Hit
	switch s.Kind {
	case KindPR, KindIssue:
		if !s.Open {
			break
		}
		if s.Kind == KindPR && s.Direction == "outgoing" && days(s.UpdatedAt) > p.OutgoingPRStaleDays {
			hits = append(hits, Hit{Rule: "outgoing-stale", Detail: fmt.Sprintf("no activity for %dd (> %dd)", days(s.UpdatedAt), p.OutgoingPRStaleDays)})
		}
		if s.Direction == "incoming" && s.LastReplyByMe.Before(s.LastActivity) && days(s.LastActivity) > p.IncomingNoReplyDays {
			hits = append(hits, Hit{Rule: "incoming-no-reply", Detail: fmt.Sprintf("waiting on you for %dd (> %dd)", days(s.LastActivity), p.IncomingNoReplyDays)})
		}
	case KindRepo:
		if !s.Archived && s.Undecided && !s.UpdatedAt.IsZero() && days(s.UpdatedAt) > p.RepoDormantDays {
			hits = append(hits, Hit{Rule: "dormant", Detail: fmt.Sprintf("not pushed for %dd (> %dd)", days(s.UpdatedAt), p.RepoDormantDays)})
		}
	case KindBranch, KindWorktree:
		if !s.OldestUnpushed.IsZero() && days(s.OldestUnpushed) > p.UnpushedDays {
			hits = append(hits, Hit{Rule: "unpushed", Detail: fmt.Sprintf("local-only commits for %dd on %s", days(s.OldestUnpushed), s.UnpushedWhere)})
		}
	}
	return hits
}
