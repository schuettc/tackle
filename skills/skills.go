// Package skills holds the agent skills tackle's tools ship, embedded so a
// binary installs the skill it was built with (sift skills install).
package skills

import "embed"

// FS holds each skill as <name>/SKILL.md.
//
//go:embed sift/SKILL.md
var FS embed.FS
