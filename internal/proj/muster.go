package proj

import (
	"encoding/json"
	"os/exec"
)

type Attention struct {
	Unread, ActionRequired int
}

type musterRow struct {
	Alias          string `json:"alias"`
	Unread         int    `json:"unread"`
	ActionRequired int    `json:"action_required"`
}

// MusterCounts returns per-alias attention counts, or an empty map when muster
// is not installed / errors / emits an unrecognized shape. Side-effect-free:
// `muster status --json` must not journal a peek.
func MusterCounts() map[string]Attention {
	if _, err := exec.LookPath("muster"); err != nil {
		return map[string]Attention{}
	}
	out, err := exec.Command("muster", "status", "--json").Output()
	if err != nil {
		return map[string]Attention{}
	}
	return parseMusterStatus(out)
}

func parseMusterStatus(b []byte) map[string]Attention {
	m := map[string]Attention{}
	var arr []musterRow
	if json.Unmarshal(b, &arr) == nil && len(arr) > 0 {
		for _, r := range arr {
			if r.Alias != "" {
				m[r.Alias] = Attention{r.Unread, r.ActionRequired}
			}
		}
		return m
	}
	var wrap struct {
		Agents []musterRow `json:"agents"`
	}
	if json.Unmarshal(b, &wrap) == nil {
		for _, r := range wrap.Agents {
			if r.Alias != "" {
				m[r.Alias] = Attention{r.Unread, r.ActionRequired}
			}
		}
	}
	return m
}
