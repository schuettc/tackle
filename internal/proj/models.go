package proj

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claudeModels is Claude Code's Anthropic model menu — index 0 is the default.
// Claude Code has no command to enumerate the models an account can reach, so
// this is maintained by hand: bump it when Anthropic ships new models.
var claudeModels = []string{
	"claude-opus-4-8",
	"claude-opus-4-7",
	"claude-sonnet-4-6",
	"claude-haiku-4-5",
}

const piModelsTTL = 24 * time.Hour

func piModelsCachePath() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".cache")
	}
	return filepath.Join(base, "proj", "pi-models.txt")
}

// piListModels returns raw `pi --list-models` output, cached at
// piModelsCachePath and rebuilt when the cache is missing or older than
// piModelsTTL. A fresh fetch that fails falls back to any stale cache, then to
// "". The ~1s fetch happens at most once per TTL; warm reads are instant.
func piListModels() string {
	p := piModelsCachePath()
	if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) < piModelsTTL {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	if !hasBin("pi") {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
		return ""
	}
	out, err := exec.Command("pi", "--list-models").Output()
	if err != nil {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
		return ""
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	_ = os.WriteFile(p, out, 0o644)
	return string(out)
}

type piModelRow struct{ provider, model string }

// parsePiRows extracts (provider, model) from the whitespace-columned
// `pi --list-models` table, skipping the header and any blank lines.
func parsePiRows(raw string) []piModelRow {
	var rows []piModelRow
	for _, line := range strings.Split(raw, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "provider" {
			continue
		}
		rows = append(rows, piModelRow{f[0], f[1]})
	}
	return rows
}

// parsePiModels filters the table to the given providers and returns
// "provider/model" ids — provider order first, then catalog order within each.
func parsePiModels(raw string, providers []string) []string {
	rows := parsePiRows(raw)
	var out []string
	for _, p := range providers {
		for _, r := range rows {
			if r.provider == p {
				out = append(out, r.provider+"/"+r.model)
			}
		}
	}
	return out
}

// ModelsForAgent returns agent's model menu (index 0 = default), or nil when
// the agent takes no model. claude is the built-in list; pi is discovered from
// the providers configured for it; cursor and none have none.
func ModelsForAgent(cfg Config, agent string) []string {
	switch agent {
	case "claude":
		return claudeModels
	case "pi":
		providers := cfg.ModelProvidersFor("pi")
		if len(providers) == 0 {
			return nil
		}
		return parsePiModels(piListModels(), providers)
	default:
		return nil
	}
}
