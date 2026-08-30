package proj

import (
	"fmt"
	"os"
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
			Print: fmt.Sprintf("exec tmux -L %s attach -t =%s", targetSocket, name)}
	case currentServer == targetSocket:
		return Action{Kind: "switch", Cmd: []string{"switch-client", "-t", "=" + name}}
	default:
		return Action{Kind: "detach", Cmd: []string{"detach", "-E",
			fmt.Sprintf("tmux -L %s attach -t =%s", targetSocket, name)}}
	}
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
