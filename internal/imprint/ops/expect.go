package ops

import (
	"context"
	"fmt"
	"strings"
)

// Expect watches for pkg@version and reports whether CI from repo signed
// it, or what signed it instead.
func (o *Ops) Expect(ctx context.Context, pkg, version, repo string, report Report) error {
	if _, err := o.watch(ctx, pkg, version, report); err != nil {
		return fail(report, err)
	}
	key := pkg + "@" + version
	pv, ok, err := o.Reg.Provenance(ctx, pkg, version)
	if err != nil {
		return fail(report, fmt.Errorf("failed: %s is live, but reading its provenance failed: %w", key, err))
	}
	if !ok {
		return fail(report, fmt.Errorf("%s is live but carries no provenance: it wasn't published by CI through a trusted publisher", key))
	}
	if !strings.EqualFold(pv.Repository, repo) {
		return fail(report, fmt.Errorf("%s is live but signed by CI from %s (%s, %s), not %s", key, pv.Repository, pv.Workflow, pv.Ref, repo))
	}
	report(fmt.Sprintf("%s is live and signed by CI from %s (%s, %s)", key, pv.Repository, pv.Workflow, pv.Ref))
	return nil
}
