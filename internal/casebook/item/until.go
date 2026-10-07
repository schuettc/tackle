package item

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Cond is a parsed until condition.
type Cond struct {
	Op   string        // date | merged | closed | inactive | released
	Date time.Time     // date
	Ref  Key           // merged, closed, released
	Dur  time.Duration // inactive
}

// Facts answers the questions until conditions ask. ok=false means unknown.
type Facts interface {
	State(k Key) (state string, ok bool)         // pr/issue: OPEN, CLOSED or MERGED
	LatestRelease(k Key) (at time.Time, ok bool) // repo
	LastActivity(k Key) (at time.Time, ok bool)  // any item
	// OthersActivity: a pr's or issue's latest activity by anyone but the
	// configured user. ok=false: none known (and always for branches,
	// worktrees and repos, which have no such activity).
	OthersActivity(k Key) (at time.Time, ok bool)
	// Missing: a sync looked k up and GitHub answered that it doesn't
	// exist. Not looked up yet, or a lookup that failed otherwise, is false.
	Missing(k Key) bool
}

var refKinds = map[string][]Kind{
	"merged":   {KindPR},
	"closed":   {KindPR, KindIssue},
	"released": {KindRepo},
}

// UntilForm describes one until operator: the symbolic syntax and one concrete
// example value. This list is the single source of truth; the page API uses it
// to build the decision vocabulary rather than maintaining its own copy.
type UntilForm struct {
	Op      string
	Syntax  string
	Example string
}

// UntilForms returns all supported until operators in declaration order,
// derived from the switch cases in ParseUntil and the refKinds map above.
func UntilForms() []UntilForm {
	return []UntilForm{
		{Op: "date", Syntax: "date(YYYY-MM-DD)", Example: "date(2026-12-01)"},
		{Op: "merged", Syntax: "merged(<pr>)", Example: "merged(pr:owner/repo#1)"},
		{Op: "closed", Syntax: "closed(<pr|issue>)", Example: "closed(issue:owner/repo#1)"},
		{Op: "inactive", Syntax: "inactive(<duration>)", Example: "inactive(90d)"},
		{Op: "released", Syntax: "released(<repo>)", Example: "released(repo:owner/repo)"},
	}
}

// ParseUntil parses date(YYYY-MM-DD), merged(<pr key>), closed(<pr|issue key>),
// inactive(<n>h|<n>d|<n>w) or released(<repo key>).
func ParseUntil(s string) (Cond, error) {
	s = strings.TrimSpace(s)
	open := strings.IndexByte(s, '(')
	if open <= 0 || !strings.HasSuffix(s, ")") {
		return Cond{}, fmt.Errorf("invalid until %q: want op(arg)", s)
	}
	op, arg := s[:open], strings.TrimSpace(s[open+1:len(s)-1])
	switch op {
	case "date":
		t, err := time.Parse("2006-01-02", arg)
		if err != nil {
			return Cond{}, fmt.Errorf("invalid until %q: date must be YYYY-MM-DD", s)
		}
		return Cond{Op: op, Date: t}, nil
	case "merged", "closed", "released":
		k, err := ParseKey(arg)
		if err != nil {
			return Cond{}, fmt.Errorf("invalid until %q: %w", s, err)
		}
		if !slices.Contains(refKinds[op], k.Kind) {
			return Cond{}, fmt.Errorf("invalid until %q: %s takes a %v key", s, op, refKinds[op])
		}
		return Cond{Op: op, Ref: k}, nil
	case "inactive":
		d, err := ParseDuration(arg)
		if err != nil {
			return Cond{}, fmt.Errorf("invalid until %q: %w", s, err)
		}
		return Cond{Op: op, Dur: d}, nil
	}
	return Cond{}, fmt.Errorf("invalid until %q: unknown op %q (date, merged, closed, inactive, released)", s, op)
}

// String renders the canonical text (durations in the largest whole unit).
func (c Cond) String() string {
	switch c.Op {
	case "date":
		return "date(" + c.Date.Format("2006-01-02") + ")"
	case "inactive":
		return "inactive(" + formatDur(c.Dur) + ")"
	}
	return c.Op + "(" + c.Ref.String() + ")"
}

// Met reports whether c holds for item it. known=false means the facts it
// needs are missing; callers treat that as not met, never as met.
func (c Cond) Met(it Key, decidedAt time.Time, f Facts, now time.Time) (met, known bool) {
	switch c.Op {
	case "date":
		return !now.Before(c.Date), true
	case "merged":
		st, ok := f.State(c.Ref)
		return ok && st == "MERGED", ok
	case "closed":
		st, ok := f.State(c.Ref)
		return ok && (st == "CLOSED" || st == "MERGED"), ok
	case "released":
		at, ok := f.LatestRelease(c.Ref)
		return ok && at.After(decidedAt), ok
	case "inactive":
		at, ok := f.LastActivity(it)
		return ok && !now.Before(at.Add(c.Dur)), ok
	}
	return false, false
}

// MissingRef returns the key a wait or watch decision's condition names when
// GitHub has said that key doesn't exist: the condition can never be met.
func MissingRef(d *Decision, f Facts) (Key, bool) {
	if d == nil || (d.Disposition != Wait && d.Disposition != Watch) {
		return Key{}, false
	}
	c, err := ParseUntil(d.Until)
	if err != nil || c.Ref.Kind == "" || !f.Missing(c.Ref) {
		return Key{}, false
	}
	return c.Ref, true
}

// ParseDuration parses casebook's duration syntax: <n>h, <n>d or <n>w.
func ParseDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("bad duration %q", s)
	}
	switch s[len(s)-1] {
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	return 0, fmt.Errorf("bad duration %q: use <n>h, <n>d or <n>w", s)
}

func formatDur(d time.Duration) string {
	h := int(d / time.Hour)
	switch {
	case h%(24*7) == 0:
		return strconv.Itoa(h/(24*7)) + "w"
	case h%24 == 0:
		return strconv.Itoa(h/24) + "d"
	}
	return strconv.Itoa(h) + "h"
}
