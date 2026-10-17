package ops

import (
	"context"
	"fmt"
	"strings"
)

// Publish publishes a tarball once the registry and npm say it can: the
// version isn't on npm or being processed, and for the first publish of a
// scoped name the user belongs to the scope's org. It then watches the
// registry until the version is live.
func (o *Ops) Publish(ctx context.Context, tarball string, report Report) error {
	abs, name, ver, err := ReadTarball(tarball)
	if err != nil {
		return fail(report, fmt.Errorf("failed: %w", err))
	}
	key := name + "@" + ver
	// One publish of a version at a time, held from the checks through the
	// watch: npm takes minutes to show a publish.
	o.mu.Lock()
	if o.pending[key] {
		o.mu.Unlock()
		return fail(report, fmt.Errorf("%s was published and npm is still processing it; wait until I say it's live", key))
	}
	if o.pending == nil {
		o.pending = map[string]bool{}
	}
	o.pending[key] = true
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.pending, key)
		o.mu.Unlock()
	}()
	p, err := o.Reg.Package(ctx, name)
	if err != nil {
		return fail(report, fmt.Errorf("failed: reading the registry: %w", err))
	}
	if p.Has(ver) {
		return fail(report, fmt.Errorf("%s is already on npm", key))
	}
	if p.Staging() {
		return fail(report, fmt.Errorf("npm is still processing a publish of %s (it shows 0.0.0-stage); check with imprint_show before publishing again", name))
	}
	if scope, ok := scopeOf(name); ok && len(p.Real()) == 0 {
		if err := o.checkOrg(ctx, scope, report); err != nil {
			return fail(report, err)
		}
	}
	if _, err := o.npm(ctx, "publish", name, []string{"publish", abs, "--access", "public", "--ignore-scripts", "--tag", "latest"}, report); err != nil {
		return fail(report, err)
	}
	report(fmt.Sprintf("npm accepted %s and is processing it, usually 3 to 6 minutes; don't work out the next version until I say it's live", key))
	if err := o.watch(ctx, name, ver, report); err != nil {
		return fail(report, err)
	}
	report("live on npm: " + key)
	return nil
}

// scopeOf is the scope of "@scope/name".
func scopeOf(name string) (string, bool) {
	rest, ok := strings.CutPrefix(name, "@")
	if !ok {
		return "", false
	}
	scope, _, ok := strings.Cut(rest, "/")
	return scope, ok && scope != ""
}

// checkOrg makes sure the npm user may publish the first package of
// scope: the scope is the user's own, or an org the user belongs to.
func (o *Ops) checkOrg(ctx context.Context, scope string, report Report) error {
	user, err := o.whoami(ctx, report)
	if err != nil {
		return err
	}
	if strings.EqualFold(user, scope) {
		return nil
	}
	res, err := o.npm(ctx, "org", scope, []string{"org", "ls", scope}, report)
	if err != nil {
		// An E404 on npm's -/v1/done poll is an expired approval, which err
		// already explains; only the org lookup's own 404 means no org.
		if res.Code == "E404" && !strings.Contains(res.Output, "-/v1/done") {
			return fmt.Errorf("there is no npm org %s; create it at npmjs.com before the first publish of @%s/…", scope, scope)
		}
		return err
	}
	for l := range strings.SplitSeq(res.Output, "\n") {
		if member, _, ok := strings.Cut(strings.TrimSpace(l), " - "); ok && strings.EqualFold(member, user) {
			return nil
		}
	}
	return fmt.Errorf("you aren't a member of the npm org %s (npm user %s); an owner adds you at npmjs.com", scope, user)
}
