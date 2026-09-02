package proj

import (
	"encoding/json"
	"os/exec"
	"strings"
)

type Attention struct {
	Unread, ActionRequired int
}

// MusterDevice returns this machine's muster device name (e.g. "personal"), or
// "" if muster is absent or the name is unset. muster prefixes every roster
// alias registered from this machine with "<device>-", so proj needs it to map
// its socket-derived "<project>/<label>" alias to muster's
// "<device>-<project>/<label>". `muster device` prints `name=<device> (...)`.
func MusterDevice() string {
	if _, err := exec.LookPath("muster"); err != nil {
		return ""
	}
	out, err := exec.Command("muster", "device").Output()
	if err != nil {
		return ""
	}
	for _, f := range strings.Fields(string(out)) {
		if rest, ok := strings.CutPrefix(f, "name="); ok {
			if rest == "(unset)" || rest == "" {
				return ""
			}
			return rest
		}
	}
	return ""
}

// AttentionFor looks up a proj-derived alias ("<project>/<label>") in muster's
// counts. muster roster aliases from this machine are device-prefixed
// ("<device>-<project>/<label>", e.g. "personal-tools-workspace/tackle"), so
// the device-prefixed key is tried first, then the bare alias (for unprefixed
// rows or when the device name is unknown). No fuzzy matching — project names
// contain hyphens, so a suffix match would be ambiguous.
func AttentionFor(counts map[string]Attention, device, alias string) Attention {
	if alias == "" {
		return Attention{}
	}
	if device != "" {
		if a, ok := counts[device+"-"+alias]; ok {
			return a
		}
	}
	return counts[alias]
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
