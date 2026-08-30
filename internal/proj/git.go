package proj

import (
	"os/exec"
	"strconv"
	"strings"
)

type GitInfo struct {
	Repo          bool
	Branch        string
	Ahead, Behind int
	Dirty         int
}

// GitStatus reports the branch, ahead/behind, and dirty-file count for dir.
// Zero value (Repo=false) when dir is not a git work tree or git is absent.
func GitStatus(dir string) GitInfo {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain=v2", "--branch").Output()
	if err != nil {
		return GitInfo{}
	}
	g := GitInfo{Repo: true}
	for _, ln := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(ln, "# branch.head "):
			g.Branch = strings.TrimPrefix(ln, "# branch.head ")
		case strings.HasPrefix(ln, "# branch.ab "):
			// "# branch.ab +A -B"
			f := strings.Fields(ln)
			if len(f) == 4 {
				g.Ahead = abs(atoiSigned(f[2]))
				g.Behind = abs(atoiSigned(f[3]))
			}
		case ln == "":
		case ln[0] == '#':
		default:
			g.Dirty++ // 1/2/u/? entries are all working-tree changes
		}
	}
	if g.Branch == "(detached)" {
		g.Branch = "detached"
	}
	return g
}

func atoiSigned(s string) int { n, _ := strconv.Atoi(strings.TrimPrefix(s, "+")); return n }
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
