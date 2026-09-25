package app

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/schuettc/tackle/internal/docket/engine"
	"github.com/schuettc/tackle/internal/docket/item"
)

// TriageEntry is one filled-in (or skipped) triage decision.
type TriageEntry struct {
	Key         string `toml:"key"`
	Disposition string `toml:"disposition"`
	Until       string `toml:"until"`
	Note        string `toml:"note"`
}

// TriageFile renders items as a TOML worksheet: fill in dispositions, then
// `docket decide --from <file>`. Evidence goes in comments.
func TriageFile(items []engine.Item, now time.Time) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# docket triage, %s: %d items.\n", now.UTC().Format("2006-01-02"), len(items))
	b.WriteString("# Fill in disposition (plus until/note when useful). Entries left \"\" are skipped.\n")
	b.WriteString("# until: date(YYYY-MM-DD) merged(<pr>) closed(<pr|issue>) inactive(90d) released(<repo>)\n")
	b.WriteString("# Apply with: docket decide --from <this file>\n")
	for _, it := range items {
		fmt.Fprintf(&b, "\n[[item]]\nkey = %s\n", tomlString(it.ID))
		line := "# status " + string(it.Status)
		if it.Relation != "" {
			line += " · " + it.Relation
		}
		if it.Title != "" {
			line += " · " + it.Title
		}
		b.WriteString(comment(line))
		for _, h := range it.Hits {
			b.WriteString(comment("# " + h.Rule + ": " + h.Detail))
		}
		for _, e := range it.Evidence {
			b.WriteString(comment("# " + e))
		}
		if it.Decision != nil {
			b.WriteString(comment(fmt.Sprintf("# currently: %s by %s", it.Decision.Disposition, it.Decision.DecidedBy)))
		}
		var allowed []string
		for _, d := range item.Allowed(it.Kind) {
			allowed = append(allowed, string(d))
		}
		b.WriteString("# allowed: " + strings.Join(allowed, " ") + "\n")
		b.WriteString("disposition = \"\"\nuntil = \"\"\nnote = \"\"\n")
	}
	return b.Bytes()
}

func comment(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) + "\n"
}

// tomlString quotes s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\u%04X", r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ParseTriage reads a worksheet, rejecting unknown fields, and returns the
// entries that have a disposition.
func ParseTriage(b []byte) ([]TriageEntry, error) {
	var f struct {
		Item []TriageEntry `toml:"item"`
	}
	md, err := toml.Decode(string(b), &f)
	if err != nil {
		return nil, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		return nil, fmt.Errorf("unknown field %q", und[0].String())
	}
	var out []TriageEntry
	for _, e := range f.Item {
		if strings.TrimSpace(e.Disposition) != "" {
			out = append(out, e)
		}
	}
	return out, nil
}
