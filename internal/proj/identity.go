package proj

import (
	"path/filepath"
	"regexp"
	"strings"
)

var workRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// tmuxTargetSafe maps the two tmux target separators to '-'. tmux parses a
// target as session:window.pane, so a '.' or ':' anywhere in a session name
// makes it unaddressable by "=name": has-session/switch-client silently miss
// it and EnsureSession then errors "duplicate session". Work names already
// exclude both (ValidWork), but project names come from dir basenames (e.g.
// ".claude") and aren't otherwise constrained, so the composed name is scrubbed
// here. muster keys a session's identity off the socket (proj-<project>) and
// the @claude_task label, not this string, so scrubbing it can't desync mail.
var tmuxTargetSafe = strings.NewReplacer(".", "-", ":", "-")

func SlugWork(s string) string { return strings.Join(strings.Fields(s), "-") }
func ValidWork(s string) bool  { return workRe.MatchString(s) }
func SessionName(project, work string) string {
	return tmuxTargetSafe.Replace(project + "/" + work)
}

func ProjectFromSocket(socket string) string {
	if socket == "" {
		return ""
	}
	base := filepath.Base(socket)
	if !strings.HasPrefix(base, "proj-") {
		return ""
	}
	return strings.TrimPrefix(base, "proj-")
}

func AliasFor(socket, label string) string {
	p := ProjectFromSocket(socket)
	if p == "" {
		return ""
	}
	return p + "/" + label
}
