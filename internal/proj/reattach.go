package proj

import (
	"fmt"
	"os"
	"strings"
)

type Action struct {
	Kind  string   // "switch" | "detach" | "print"
	Cmd   []string // tmux args for switch/detach (run on the CURRENT server)
	Print string   // shell line for the shim to eval (print case)
}

func PlanGoto(currentServer, targetSocket, name string) Action {
	switch {
	case currentServer == "":
		return Action{Kind: "print",
			Print: fmt.Sprintf("exec tmux -L %s attach -t %s", targetSocket, quoteTarget(name))}
	case currentServer == targetSocket:
		return Action{Kind: "switch", Cmd: []string{"switch-client", "-t", "=" + name}}
	default:
		return Action{Kind: "detach", Cmd: []string{"detach", "-E",
			fmt.Sprintf("tmux -L %s attach -t %s", targetSocket, quoteTarget(name))}}
	}
}

// quoteTarget single-quotes the tmux `=name` exact-match target for the two
// paths whose command string is re-parsed by a shell (the emitted `print` line
// the zsh shim evals, and the `detach -E` command tmux runs via default-shell,
// which is zsh here). The leading `=` MUST live inside the quotes: an unquoted
// leading `=` triggers zsh's EQUALS filename expansion (`=word` -> path of
// command `word`), which aborts before tmux runs because a proj session name
// like `tools-workspace/proj` is not a command. The switch-client path is a
// direct exec.Command arg (no shell) and is deliberately left unquoted.
func quoteTarget(name string) string {
	return "'=" + strings.ReplaceAll(name, "'", `'\''`) + "'"
}

// Goto executes the planned action. switch/detach run on the CURRENT server
// (via $TMUX, no -L). print writes the exec line to stdout for the shim.
func Goto(targetSocket, name string) error {
	a := PlanGoto(CurrentServer(), targetSocket, name)
	switch a.Kind {
	case "print":
		if p := os.Getenv("PROJ_EXEC"); p != "" {
			return os.WriteFile(p, []byte(a.Print+"\n"), 0o644)
		}
		fmt.Println(a.Print)
		return nil
	default:
		_, err := Run("", a.Cmd...) // current server via ambient $TMUX
		return err
	}
}
