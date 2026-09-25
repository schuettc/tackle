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
}

var refKinds = map[string][]Kind{
	"merged":   {KindPR},
	"closed":   {KindPR, KindIssue},
	"released": {KindRepo},
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
		d, err := parseDur(arg)
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

func parseDur(s string) (time.Duration, error) {
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
