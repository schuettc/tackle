package ops

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Summary is what the registry says of a package.
type Summary struct {
	Name     string
	Exists   bool
	Latest   string
	DistTags map[string]string
	Recent   []VersionInfo // the newest five, newest first
	Staging  bool          // npm is still processing a publish
}

// VersionInfo is one version: its deprecation and who signed it.
type VersionInfo struct {
	Number, Deprecated string
	Signed             string // the repo whose CI signed it, "" for none
	Workflow, Ref      string
}

// String is the summary as the agent reads it.
func (s Summary) String() string {
	if !s.Exists {
		return s.Name + " is not on npm"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\nlatest: %s\n", s.Name, orNone(s.Latest))
	var tags []string
	for t, v := range s.DistTags {
		if t != "latest" {
			tags = append(tags, t+": "+v)
		}
	}
	sort.Strings(tags)
	if len(tags) > 0 {
		fmt.Fprintf(&b, "other dist-tags: %s\n", strings.Join(tags, ", "))
	}
	if s.Staging {
		b.WriteString("npm is still processing a publish (it shows 0.0.0-stage): don't publish or work out the next version until it is live\n")
	}
	if len(s.Recent) == 0 {
		b.WriteString("no versions yet\n")
	}
	for _, v := range s.Recent {
		line := v.Number
		if v.Signed != "" {
			line += fmt.Sprintf(" signed by CI from %s (%s, %s)", v.Signed, v.Workflow, v.Ref)
		} else {
			line += " no provenance"
		}
		if v.Deprecated != "" {
			line += fmt.Sprintf("; %s deprecated: %s", v.Number, v.Deprecated)
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Show reads pkg from the registry: no npm, no approval.
func (o *Ops) Show(ctx context.Context, pkg string) (Summary, error) {
	p, err := o.Reg.Package(ctx, pkg)
	if err != nil {
		return Summary{Name: pkg}, err
	}
	s := Summary{Name: pkg, Exists: p.Exists, Latest: p.DistTags["latest"], DistTags: p.DistTags, Staging: p.Staging()}
	real := p.Real()
	for i := len(real) - 1; i >= 0 && len(s.Recent) < 5; i-- {
		v := real[i]
		vi := VersionInfo{Number: v.Number, Deprecated: v.Deprecated}
		if v.HasProvenance {
			if pv, ok, err := o.Reg.Provenance(ctx, pkg, v.Number); err == nil && ok {
				vi.Signed, vi.Workflow, vi.Ref = pv.Repository, pv.Workflow, pv.Ref
			}
		}
		s.Recent = append(s.Recent, vi)
	}
	return s, nil
}

// ShowTrust reports pkg's trusted publisher (npm trust list, which may
// need the user's approval) and whether its latest version proves it.
func (o *Ops) ShowTrust(ctx context.Context, pkg string, report Report) error {
	have, err := o.trustList(ctx, pkg, report)
	if err != nil {
		return fail(report, err)
	}
	if len(have) == 0 {
		report(pkg + " has no trusted publisher")
		return nil
	}
	p, err := o.Reg.Package(ctx, pkg)
	if err != nil {
		return fail(report, fmt.Errorf("failed: reading the registry: %w", err))
	}
	latest := p.DistTags["latest"]
	for _, t := range have {
		msg := fmt.Sprintf("%s trusted publisher: %s %s (%s)", pkg, orNone(t.Type), t.Repository, t.File)
		pv, ok, err := o.Reg.Provenance(ctx, pkg, latest)
		switch {
		case latest == "":
			msg += "; unproven: no version is published yet"
		case err == nil && ok && t.matches(pv.Repository, pv.Workflow):
			msg += fmt.Sprintf("; proven by %s@%s", pkg, latest)
		default:
			msg += fmt.Sprintf("; unproven: the latest version, %s, wasn't published by CI from %s; run one CI release and confirm it with imprint_expect", latest, t.Repository)
		}
		report(msg)
	}
	return nil
}
