package observe

import "testing"

func TestGitHubRepo(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Acme/Widget.git":          "acme/widget",
		"https://github.com/acme/widget":              "acme/widget",
		"https://github.com/acme/widget/":             "acme/widget",
		"https://x-access-token:t@github.com/a/b.git": "a/b",
		"git@github.com:Acme/Widget.git":              "acme/widget",
		"ssh://git@github.com/a/b.git":                "a/b",
		"ssh://git@ssh.github.com:443/a/b.git":        "a/b",
		"git@github.com-work:a/b.git":                 "a/b",
		"git://github.com/a/b":                        "a/b",
		"git@gitlab.com:a/b.git":                      "",
		"https://github.company.com/a/b.git":          "",
		"/Users/c/remote.git":                         "",
		"https://github.com/a":                        "",
	}
	for in, want := range cases {
		if got := GitHubRepo(in); got != want {
			t.Errorf("GitHubRepo(%q) = %q, want %q", in, got, want)
		}
	}
}
