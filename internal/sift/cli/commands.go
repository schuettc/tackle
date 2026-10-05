package cli

import (
	"io"

	tools "github.com/schuettc/tools-common"
)

func commands(stdin io.Reader) []tools.Command {
	_ = stdin
	return []tools.Command{}
}
