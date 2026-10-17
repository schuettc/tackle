package ops

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/subaud-tools/tackle/internal/imprint/registry"
)

// readBack is how many times a change is read back from the registry,
// Poll apart, before imprint says it isn't showing.
const readBack = 8

// undeprecated is each real version not carrying message.
func undeprecated(p registry.Package, message string) []string {
	var out []string
	for _, v := range p.Real() {
		if v.Deprecated != message {
			out = append(out, v.Number)
		}
	}
	return out
}

// Deprecate marks every version of pkg with message: nothing when each
// already carries it; otherwise npm deprecate, done once the registry
// shows the message on every version.
func (o *Ops) Deprecate(ctx context.Context, pkg, message string, report Report) error {
	p, err := o.Reg.Package(ctx, pkg)
	if err != nil {
		return fail(report, fmt.Errorf("failed: reading the registry: %w", err))
	}
	if !p.Exists || len(p.Real()) == 0 {
		return fail(report, fmt.Errorf("%s has no published versions to deprecate", pkg))
	}
	if len(undeprecated(p, message)) == 0 {
		report(fmt.Sprintf("%s is already deprecated with that message; nothing to change", pkg))
		return nil
	}
	if _, err := o.npm(ctx, "deprecate", pkg, []string{"deprecate", pkg, message}, report); err != nil {
		return fail(report, err)
	}
	var left []string
	for i := range readBack {
		if i > 0 {
			select {
			case <-ctx.Done():
				return fail(report, ctx.Err())
			case <-time.After(o.Poll):
			}
		}
		p, err = o.Reg.Package(ctx, pkg)
		if err != nil {
			continue
		}
		if left = undeprecated(p, message); len(left) == 0 {
			report(fmt.Sprintf("deprecated every version of %s: %q", pkg, message))
			return nil
		}
	}
	return fail(report, fmt.Errorf("npm deprecate returned, but the registry doesn't show the message on %s@%s yet; check with imprint_show", pkg, strings.Join(left, ", ")))
}
