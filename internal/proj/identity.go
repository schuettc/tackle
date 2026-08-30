package proj

import (
	"path/filepath"
	"regexp"
	"strings"
)

var workRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func SlugWork(s string) string                { return strings.Join(strings.Fields(s), "-") }
func ValidWork(s string) bool                 { return workRe.MatchString(s) }
func SessionName(project, work string) string { return project + "/" + work }

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
