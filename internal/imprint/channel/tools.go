package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/subaud-tools/tackle/internal/imprint/ops"
	"github.com/subaud-tools/tools-common/channelmcp"
)

func schema(s string) json.RawMessage { return json.RawMessage(s) }

// object is a JSON schema of string properties, each required.
func object(props ...[2]string) json.RawMessage {
	var ps, req []string
	for _, p := range props {
		ps = append(ps, fmt.Sprintf("%q:{\"type\":\"string\",\"description\":%q}", p[0], p[1]))
		req = append(req, fmt.Sprintf("%q", p[0]))
	}
	return schema(`{"type":"object","properties":{` + strings.Join(ps, ",") + `},"required":[` + strings.Join(req, ",") + `]}`)
}

var (
	pkgProp      = [2]string{"package", "the npm package name, e.g. @scope/name"}
	repoProp     = [2]string{"repo", "the GitHub repo, owner/name"}
	workflowProp = [2]string{"workflow", "the workflow file name in .github/workflows, e.g. release.yml"}
)

// Tools is imprint's tool list.
func Tools() []channelmcp.Tool {
	return []channelmcp.Tool{
		{Name: "imprint_login", Description: "Make sure npm is logged in: npm whoami, and only when it isn't logged in, a web login whose link imprint opens for the user to approve. Returns at once; reports \"logged in as <user>\" as a message.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
		{Name: "imprint_show", Description: "What the registry says of a package, at once and with no approval: latest version, dist-tags, the newest versions with their deprecation and the repo whose CI signed each, and whether npm is still processing a publish (0.0.0-stage). Then reads its trusted publisher with npm trust list (which may need the user's approval) and sends it as a message, saying when no CI release has proven it.",
			InputSchema: object(pkgProp)},
		{Name: "imprint_publish", Description: "Publish a tarball the repo's build made: imprint resolves the path, reads the name and version, refuses a version already on npm or still processing, checks org membership before a scoped package's first publish, and runs npm publish (public, no scripts, tag latest). Returns at once; messages follow until the version is live on npm, usually 3 to 6 minutes after npm accepts it.",
			InputSchema: object([2]string{"tarball", "the path of the .tgz to publish"})},
		{Name: "imprint_trust", Description: "Make a GitHub repo's workflow the package's one trusted publisher: nothing when it already is; otherwise each existing trusted publisher is revoked, the new one created and read back. The package must already exist on npm. Returns at once; a new trusted publisher is unproven until one CI release is confirmed with imprint_expect.",
			InputSchema: object(pkgProp, repoProp, workflowProp)},
		{Name: "imprint_deprecate", Description: "Deprecate every version of a package with a message: nothing when each already carries it; done once the registry shows the message. Returns at once; messages follow.",
			InputSchema: object(pkgProp, [2]string{"message", "the deprecation message, e.g. where the package moved"})},
		{Name: "imprint_expect", Description: "Watch the registry for a version a CI release is publishing, and report when it is live and signed by CI from the given repo, or what signed it instead (no provenance, another repo). Returns at once; messages follow.",
			InputSchema: object(pkgProp, [2]string{"version", "the version the release publishes"}, repoProp)},
		{Name: "imprint_status", Description: "What is running now: each operation waiting on the user's approval or on the registry, with its last message.",
			InputSchema: schema(`{"type":"object","properties":{}}`)},
	}
}

type args struct {
	Package  string `json:"package"`
	Tarball  string `json:"tarball"`
	Repo     string `json:"repo"`
	Workflow string `json:"workflow"`
	Message  string `json:"message"`
	Version  string `json:"version"`
}

// need fails unless each named argument is set.
func need(pairs ...string) error {
	var missing []string
	for i := 0; i+1 < len(pairs); i += 2 {
		if strings.TrimSpace(pairs[i+1]) == "" {
			missing = append(missing, pairs[i])
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

// Call runs one tool.
func (ch *Channel) Call(name string, raw json.RawMessage) (string, error) {
	var a args
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &a); err != nil {
			return "", fmt.Errorf("arguments: %w", err)
		}
	}
	o := ch.Ops
	switch name {
	case "imprint_login":
		return ch.start(name, "", "npm login check", o.Login), nil
	case "imprint_show":
		if err := need("package", a.Package); err != nil {
			return "", err
		}
		s, err := o.Show(ch.ctx(), a.Package)
		if err != nil {
			return "", fmt.Errorf("reading the registry: %w", err)
		}
		if !s.Exists {
			return s.String(), nil
		}
		started := ch.start(name, a.Package, "reading the trusted publisher of "+a.Package, func(ctx context.Context, r ops.Report) error {
			return o.ShowTrust(ctx, a.Package, r)
		})
		return s.String() + "\n\n" + started, nil
	case "imprint_publish":
		if err := need("tarball", a.Tarball); err != nil {
			return "", err
		}
		abs, pkg, ver, err := ops.ReadTarball(a.Tarball)
		if err != nil {
			return "", err
		}
		return ch.start(name, pkg, "publishing "+pkg+"@"+ver+" from "+abs, func(ctx context.Context, r ops.Report) error {
			return o.Publish(ctx, abs, r)
		}), nil
	case "imprint_trust":
		if err := need("package", a.Package, "repo", a.Repo, "workflow", a.Workflow); err != nil {
			return "", err
		}
		return ch.start(name, a.Package, "making "+a.Repo+" ("+a.Workflow+") the trusted publisher of "+a.Package, func(ctx context.Context, r ops.Report) error {
			return o.Trust(ctx, a.Package, a.Repo, a.Workflow, r)
		}), nil
	case "imprint_deprecate":
		if err := need("package", a.Package, "message", a.Message); err != nil {
			return "", err
		}
		return ch.start(name, a.Package, "deprecating "+a.Package, func(ctx context.Context, r ops.Report) error {
			return o.Deprecate(ctx, a.Package, a.Message, r)
		}), nil
	case "imprint_expect":
		if err := need("package", a.Package, "version", a.Version, "repo", a.Repo); err != nil {
			return "", err
		}
		return ch.start(name, a.Package, "watching for "+a.Package+"@"+a.Version+" from "+a.Repo, func(ctx context.Context, r ops.Report) error {
			return o.Expect(ctx, a.Package, a.Version, a.Repo, r)
		}), nil
	case "imprint_status":
		return ch.status(), nil
	}
	return "", errors.New("unknown tool " + name)
}
