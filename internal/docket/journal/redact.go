package journal

import (
	"net/url"
	"regexp"
	"strings"
)

var scpLike = regexp.MustCompile(`^([^@/:]+)@([^@/:]+):(.*)$`)

// RedactURL strips credentials from a remote URL: userinfo, query and
// fragment of a URL, and any user other than "git" in scp-style
// user@host:path. Anything that isn't a URL is returned unchanged.
func RedactURL(s string) string {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "<unparseable url>"
		}
		u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
		return u.String()
	}
	if m := scpLike.FindStringSubmatch(s); m != nil && m[1] != "git" {
		return m[2] + ":" + m[3]
	}
	return s
}
