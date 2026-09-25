package journal

import "testing"

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://x-access-token:ghs_SECRET@github.com/a/b.git": "https://github.com/a/b.git",
		"https://ghp_SECRET@github.com/a/b":                    "https://github.com/a/b",
		"https://github.com/a/b?access_token=SECRET#frag":      "https://github.com/a/b",
		"ssh://git@github.com/a/b.git":                         "ssh://github.com/a/b.git",
		"git@github.com:a/b.git":                               "git@github.com:a/b.git",
		"tok_SECRET@github.com:a/b.git":                        "github.com:a/b.git",
		"origin":                                               "origin",
		"/Users/c/remote.git":                                  "/Users/c/remote.git",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}
