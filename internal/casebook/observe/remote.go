package observe

import (
	"net/url"
	"regexp"
	"strings"
)

var scpGitHub = regexp.MustCompile(`^[^@/]+@(github\.com[^:]*):(.+)$`)

// GitHubRepo returns the lower-case "owner/name" a remote URL points at, or ""
// if it isn't a github.com remote. SSH host aliases such as github.com-work
// count as GitHub.
func GitHubRepo(remote string) string {
	var host, p string
	if m := scpGitHub.FindStringSubmatch(remote); m != nil {
		host, p = m[1], m[2]
	} else if strings.Contains(remote, "://") {
		u, err := url.Parse(remote)
		if err != nil {
			return ""
		}
		host, p = u.Hostname(), u.Path
	} else {
		return ""
	}
	if host != "github.com" && host != "ssh.github.com" && !strings.HasPrefix(host, "github.com-") {
		return ""
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(strings.Trim(p, "/"), ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return strings.ToLower(parts[0] + "/" + parts[1])
}
