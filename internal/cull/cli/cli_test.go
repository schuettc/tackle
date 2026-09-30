package cli

import (
	"bytes"
	"strings"
	"testing"
)

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errw)
	return code, out.String(), errw.String()
}

// Calibration lives in the private harness, not the public tool.
func TestCalibrationCommandsAreGone(t *testing.T) {
	for _, name := range []string{"corpus", "label", "eval"} {
		code, _, errw := run(t, "", name)
		if code != 2 || !strings.Contains(errw, "unknown command") {
			t.Errorf("%s: code %d, errw %q", name, code, errw)
		}
	}
}
