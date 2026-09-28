package rules

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/schuettc/tackle/internal/casebook/engine"
	"github.com/schuettc/tackle/internal/casebook/item"
	"github.com/schuettc/tackle/internal/casebook/propose"
)

// Match is a single item that a Rule matched and why.
type Match struct {
	Key    string // item ID
	Reason string // human-readable grouping label (e.g. "in main", "via merged PR")
}

// TrackRecord summarises how a rule's past proposals fared.
type TrackRecord struct {
	Accepted int
	Rejected int
	Pending  int
}

// RuleRecord returns the track record for rule id by counting proposals whose
// source is "rule:<id>".
func RuleRecord(ctx context.Context, s *propose.Store, id string) (TrackRecord, error) {
	t, err := s.Tally(ctx, "rule:"+id, time.Time{})
	if err != nil {
		return TrackRecord{}, err
	}
	// Accepted + Changed both count as accepted (Changed means Court accepted
	// but chose a different disposition).
	return TrackRecord{
		Accepted: t.Accepted + t.Changed,
		Rejected: t.Rejected,
		Pending:  t.Pending,
	}, nil
}

// Eval evaluates a single condition against the pre-computed field set f.
// It assumes the condition is already validated (ValidateCondition passed).
// For the "matches" operator, it compiles the regex on each call; callers
// that evaluate many items should use the compiled path in MatchAll instead.
func (c Condition) Eval(f engine.Fields) (bool, error) {
	switch c.Field {
	case "kind":
		return evalString(c.Op, f.Kind, c.Value)
	case "repo":
		return evalString(c.Op, f.Repo, c.Value)
	case "owner":
		return evalString(c.Op, f.Owner, c.Value)
	case "relation":
		return evalString(c.Op, f.Relation, c.Value)
	case "status":
		return evalString(c.Op, f.Status, c.Value)
	case "direction":
		return evalString(c.Op, f.Direction, c.Value)
	case "author":
		return evalString(c.Op, f.Author, c.Value)
	case "bot":
		return evalBool(c.Op, f.Bot, c.Value)
	case "title":
		if c.Op == "matches" {
			re, err := regexp.Compile(c.Value)
			if err != nil {
				return false, fmt.Errorf("title matches: bad regex %q: %w", c.Value, err)
			}
			return re.MatchString(f.Title), nil
		}
		return evalString(c.Op, f.Title, c.Value)
	case "label":
		return evalMultiString(c.Op, f.Labels, c.Value)
	case "age":
		return evalDuration(c.Op, f.Age, c.Value)
	case "pushed":
		return evalDuration(c.Op, f.Pushed, c.Value)
	case "updated":
		return evalDuration(c.Op, f.Updated, c.Value)
	case "landed":
		return evalString(c.Op, f.Landed, c.Value)
	case "landed-how":
		return evalMultiString(c.Op, f.LandedHow, c.Value)
	case "gone-upstream":
		return evalBool(c.Op, f.GoneUpstream, c.Value)
	case "unpushed":
		return evalBool(c.Op, f.Unpushed, c.Value)
	case "dirty":
		return evalBool(c.Op, f.Dirty, c.Value)
	case "worktree":
		return evalString(c.Op, f.Worktree, c.Value)
	case "archived":
		return evalBool(c.Op, f.Archived, c.Value)
	case "fork":
		return evalBool(c.Op, f.Fork, c.Value)
	case "open-prs":
		return evalCount(c.Op, f.OpenPRs, c.Value)
	case "open-issues":
		return evalCount(c.Op, f.OpenIssues, c.Value)
	case "has-decision":
		return evalBool(c.Op, f.HasDecision, c.Value)
	case "policy-hit":
		return evalMultiString(c.Op, f.PolicyHits, c.Value)
	default:
		return false, fmt.Errorf("unknown field %q", c.Field)
	}
}

// evalString handles is / is-not / in / not-in on a single string value.
func evalString(op, val, cval string) (bool, error) {
	switch op {
	case "is":
		return val == cval, nil
	case "is-not":
		return val != cval, nil
	case "in":
		return slices.Contains(splitIn(cval), val), nil
	case "not-in":
		return !slices.Contains(splitIn(cval), val), nil
	}
	return false, fmt.Errorf("unsupported string op %q", op)
}

// evalBool handles is / is-not on a bool value.
func evalBool(op string, val bool, cval string) (bool, error) {
	want := cval == "true"
	switch op {
	case "is":
		return val == want, nil
	case "is-not":
		return val != want, nil
	}
	return false, fmt.Errorf("unsupported bool op %q", op)
}

// evalDuration handles older-than / newer-than on a duration value.
// "older-than 7d" means the duration is greater than 7d (e.g. Age > 7d).
func evalDuration(op string, dur time.Duration, cval string) (bool, error) {
	threshold, err := item.ParseDuration(strings.TrimSpace(cval))
	if err != nil {
		return false, err
	}
	switch op {
	case "older-than":
		return dur > threshold, nil
	case "newer-than":
		return dur < threshold, nil
	}
	return false, fmt.Errorf("unsupported duration op %q", op)
}

// evalCount handles is / is-not / gt / gte / lt / lte on an int value.
func evalCount(op string, val int, cval string) (bool, error) {
	n, err := strconv.Atoi(strings.TrimSpace(cval))
	if err != nil {
		return false, fmt.Errorf("invalid count value %q: %w", cval, err)
	}
	switch op {
	case "is":
		return val == n, nil
	case "is-not":
		return val != n, nil
	case "gt":
		return val > n, nil
	case "gte":
		return val >= n, nil
	case "lt":
		return val < n, nil
	case "lte":
		return val <= n, nil
	}
	return false, fmt.Errorf("unsupported count op %q", op)
}

// evalMultiString handles is / is-not / in / not-in on a slice of strings
// (e.g. labels, policy-hits). "is X" is true when any element equals X.
func evalMultiString(op string, vals []string, cval string) (bool, error) {
	switch op {
	case "is":
		return slices.Contains(vals, cval), nil
	case "is-not":
		return !slices.Contains(vals, cval), nil
	case "in":
		for _, want := range splitIn(cval) {
			if slices.Contains(vals, want) {
				return true, nil
			}
		}
		return false, nil
	case "not-in":
		for _, want := range splitIn(cval) {
			if slices.Contains(vals, want) {
				return false, nil
			}
		}
		return true, nil
	}
	return false, fmt.Errorf("unsupported multi-string op %q", op)
}

// regexEntry pairs a compiled regex with the field it applies to (fix D).
type regexEntry struct {
	field string
	re    *regexp.Regexp
}

// compiledRule pairs a rule's conditions with pre-compiled regexes for the
// "matches" operator. Regexes are compiled once per MatchAll call and
// remember which field they match against.
type compiledRule struct {
	conds []Condition
	regs  map[int]regexEntry // index → {field, *regexp.Regexp}
}

// compileRule pre-compiles any "matches" conditions in r.Match.
func compileRule(r Rule) (compiledRule, error) {
	regs := map[int]regexEntry{}
	for i, c := range r.Match {
		if c.Op == "matches" {
			re, err := regexp.Compile(c.Value)
			if err != nil {
				return compiledRule{}, fmt.Errorf("condition %d: matches: %w", i, err)
			}
			regs[i] = regexEntry{field: c.Field, re: re}
		}
	}
	return compiledRule{conds: r.Match, regs: regs}, nil
}

// regexFieldValue returns the string value of a text field from f for regex
// matching. Only text fields support the "matches" operator (fix D).
func regexFieldValue(f engine.Fields, field string) string {
	switch field {
	case "title":
		return f.Title
	case "repo":
		return f.Repo
	case "owner":
		return f.Owner
	case "author":
		return f.Author
	case "relation":
		return f.Relation
	case "direction":
		return f.Direction
	case "status":
		return f.Status
	case "worktree":
		return f.Worktree
	case "landed":
		return f.Landed
	case "kind":
		return f.Kind
	default:
		return ""
	}
}

// evalAll returns true only if every condition holds for f.
func (cr compiledRule) evalAll(f engine.Fields) (bool, error) {
	for i, c := range cr.conds {
		var ok bool
		if entry, isMatch := cr.regs[i]; isMatch {
			ok = entry.re.MatchString(regexFieldValue(f, entry.field))
		} else {
			var err error
			ok, err = c.Eval(f)
			if err != nil {
				return false, err
			}
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// howReason maps a slice of machine-readable LandedVia codes to a
// human-readable reason for grouping matches on the page (spec §4.3).
func howReason(codes []string) string {
	if len(codes) == 0 {
		return ""
	}
	out := make([]string, 0, len(codes))
	for _, p := range codes {
		switch p {
		case "default-branch":
			out = append(out, "in main")
		case "merged-pr":
			out = append(out, "via merged PR")
		default:
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return strings.Join(out, "; ")
}

// MatchAll returns all items in res where every condition holds and the key is
// not excluded by r.Exclude. A rule whose conditions contain an invalid
// condition returns the error rather than matching.
//
// The reason in each Match reflects how the item landed (for branch rules)
// or is empty for other rule types.
func (r Rule) MatchAll(res engine.Result, now time.Time) ([]Match, error) {
	cr, err := compileRule(r)
	if err != nil {
		return nil, err
	}

	// Build exclusion set.
	excluded := make(map[string]bool, len(r.Exclude))
	for _, ex := range r.Exclude {
		excluded[ex.Key] = true
	}

	var out []Match
	for _, it := range res.Items {
		if excluded[it.ID] {
			continue
		}
		f := it.Fields(now)
		ok, err := cr.evalAll(f)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, Match{
			Key:    it.ID,
			Reason: howReason(f.LandedHow),
		})
	}
	return out, nil
}

// Proposable returns all matches that are eligible for proposal:
//   - the item has no decision (undecided)
//   - the item has no pending proposal from this rule
//   - the item was not rejected for this rule since the rule's last edit
//
// pending is keyed by item key and contains only pending proposals.
// rejected is a pre-built set of item keys rejected for this rule since
// rule.EditedAt; a rejection before EditedAt does not block re-proposing.
func (r Rule) Proposable(res engine.Result, now time.Time, pending map[string]propose.Proposal, rejected map[string]bool) []Match {
	matches, err := r.MatchAll(res, now)
	if err != nil {
		return nil
	}

	source := "rule:" + r.ID
	var out []Match
	for _, m := range matches {
		// Skip decided items.
		it, ok := res.Find(m.Key)
		if !ok {
			continue
		}
		if it.Decision != nil {
			continue
		}
		// Skip items with a pending proposal from this rule.
		if p, hasPending := pending[m.Key]; hasPending && p.Source == source {
			continue
		}
		// Skip items rejected for this rule since the rule's last edit.
		if rejected[m.Key] {
			continue
		}
		out = append(out, m)
	}
	return out
}
