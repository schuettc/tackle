package npm

import (
	"strings"
	"testing"
)

// npm 11's own wording for the failures imprint explains.
const (
	out403 = "npm notice Publishing to https://registry.npmjs.org/ with tag latest and public access\n" +
		"npm error code E403\n" +
		"npm error 403 403 Forbidden - PUT https://registry.npmjs.org/demo-pkg - You cannot publish over the previously published versions: 1.0.0.\n" +
		"npm error A complete log of this run can be found in: /tmp/_logs/debug-0.log\n"
	out404Done = "deprecating demo-pkg@1.0.0\n" +
		"Authenticate your account at:\nhttps://www.npmjs.com/auth/cli/0b1c\n" +
		"npm error code E404\n" +
		"npm error 404 Not Found - GET https://registry.npmjs.org/-/v1/done?authId=0b1c - Not found\n"
	out404Pkg = "npm error code E404\n" +
		"npm error 404 Not Found - GET https://registry.npmjs.org/demo-missing - Not found\n"
	outNeedAuth = "npm error code ENEEDAUTH\n" +
		"npm error need auth This command requires you to be logged in to https://registry.npmjs.org/\n" +
		"npm error need auth You need to authorize this machine using `npm adduser`\n"
	out401 = "npm error code E401\n" +
		"npm error 401 Unauthorized - GET https://registry.npmjs.org/-/whoami\n"
)

func TestErrorCode(t *testing.T) {
	for out, want := range map[string]string{
		out403: "E403", out404Done: "E404", out404Pkg: "E404", outNeedAuth: "ENEEDAUTH", out401: "E401",
		"+ demo-pkg@1.0.0\n": "",
	} {
		if got := ErrorCode(out); got != want {
			t.Errorf("ErrorCode = %q, want %q for:\n%s", got, want, out)
		}
	}
}

func TestExplain(t *testing.T) {
	for _, c := range []struct{ out, want string }{
		{out403, "already exists or is still processing"},
		{out404Done, "approval expired"},
		{outNeedAuth, "imprint_login"},
		{out401, "imprint_login"},
	} {
		got := Explain(ErrorCode(c.out), c.out)
		if !strings.Contains(got, c.want) {
			t.Errorf("Explain = %q, want it to say %q", got, c.want)
		}
	}
	// A plain 404 on a package is not an expired approval.
	if got := Explain("E404", out404Pkg); got != "" {
		t.Errorf("plain 404 explained as %q", got)
	}
}

// TestCleanSpinnerRedraw uses the byte layout real npm 11.19.1 printed for
// `npm trust list` under a pty: its spinner redraws with CSI 1G (cursor to
// column 1) and CSI 0K, not a carriage return, so the first field of a block
// arrives after a spinner frame on the same line.
func TestCleanSpinnerRedraw(t *testing.T) {
	for raw, want := range map[string]string{
		"\u2826\x1b[1G\x1b[0Ktype: \x1b[32mgithub\x1b[39m\r":                  "type: github",
		"\x1b[0mrepository\x1b[0m: \x1b[32macme/widgets\x1b[39m\r":            "repository: acme/widgets",
		"\u2819\x1b[1G\x1b[0K\u2839\x1b[1G\x1b[0K\x1b[1mnpm\x1b[22m notice x": "npm notice x",
	} {
		if got := clean(raw); got != want {
			t.Errorf("clean(%q) = %q, want %q", raw, got, want)
		}
	}
}
