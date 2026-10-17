package npm

import (
	"regexp"
	"strings"
)

var (
	codeRE = regexp.MustCompile(`(?m)^npm (?:error|ERR!) code (\S+)`)
	// ansiRE matches CSI and OSC escape sequences.
	ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
)

// ErrorCode is npm's error code in output ("npm error code E403" → "E403"),
// "" when npm reported none.
func ErrorCode(output string) string {
	m := codeRE.FindStringSubmatch(output)
	if m == nil {
		return ""
	}
	return m[1]
}

// Explain is what an npm error code usually means for the agent, "" when
// imprint has nothing to add to npm's own words.
func Explain(code, output string) string {
	switch {
	case code == "E401" || code == "ENEEDAUTH":
		return "npm isn't logged in; run imprint_login and the user approves the login in the browser"
	case code == "E404" && strings.Contains(output, "-/v1/done"):
		return "the approval expired before the user gave it; run it again"
	case code == "E403" && strings.Contains(strings.ToLower(output), "cannot publish over"):
		return "that version already exists or is still processing; check with imprint_show before choosing another"
	}
	return ""
}

// clean is one line of terminal output as plain text: escape sequences
// gone, and of a line redrawn with carriage returns only its last drawing.
func clean(line string) string {
	line = ansiRE.ReplaceAllString(line, "")
	line = strings.TrimRight(line, "\r")
	if i := strings.LastIndex(line, "\r"); i >= 0 {
		line = line[i+1:]
	}
	return line
}

// approvalPrompt reports whether line is npm's lead-in to an approval
// link, and the link when npm put it on the same line.
func approvalPrompt(line string) (bool, string) {
	for _, p := range []string{"Authenticate your account at:", "Login at:"} {
		if i := strings.Index(line, p); i >= 0 {
			return true, strings.TrimSpace(line[i+len(p):])
		}
	}
	return false, ""
}
