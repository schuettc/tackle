package rules

// Examples are the starter rules a new casebook-data gets, on (Court,
// 2026-10-17: examples start on; a rule is on once saved). They are ordinary
// files from then on: edited, turned off or deleted like any other.
func Examples() []Rule {
	out := []Rule{
		{
			ID:   "landed-branches",
			Name: "Landed branches",
			Match: []Condition{
				{Field: "kind", Op: "is", Value: "branch"},
				{Field: "landed", Op: "is", Value: "all-machines"},
				{Field: "unpushed", Op: "is", Value: "false"},
			},
			Propose: RuleAction{Disposition: "delete", Note: "landed {how}; nothing unpushed"},
		},
		{
			ID:   "quiet-bot-prs",
			Name: "Bot PRs quiet for 30 days",
			Match: []Condition{
				{Field: "kind", Op: "is", Value: "pr"},
				{Field: "bot", Op: "is", Value: "true"},
				{Field: "updated", Op: "older-than", Value: "30d"},
			},
			Propose: RuleAction{Disposition: "close", Note: "a bot's PR with no activity for 30 days"},
		},
		{
			ID:   "dormant-repos",
			Name: "Repos not pushed in a year",
			Match: []Condition{
				{Field: "kind", Op: "is", Value: "repo"},
				{Field: "updated", Op: "older-than", Value: "52w"},
			},
			Propose: RuleAction{Disposition: "ignore", Note: "not pushed in a year"},
		},
	}
	for i := range out {
		out[i].Status = StatusActive
		out[i].CreatedBy = "casebook"
	}
	return out
}
