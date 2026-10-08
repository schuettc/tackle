package cli

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func timeModule(t *testing.T, body string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/tm\n\ngo 1.22\n")
	writeFixture(t, root, "p/p_test.go", body)
	t.Chdir(root)
	return root
}

func TestTimeExitCodes(t *testing.T) {
	t.Run("0 and a table", func(t *testing.T) {
		timeModule(t, "package p\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) {}\n")
		code, out, errw := run(t, "", "time")
		if code != 0 {
			t.Fatalf("code %d, errw %q, out %q", code, errw, out)
		}
		for _, want := range []string{"total", "example.com/tm/p", "TestP"} {
			if !strings.Contains(out, want) {
				t.Errorf("out missing %q:\n%s", want, out)
			}
		}
	})
	t.Run("1 and json", func(t *testing.T) {
		timeModule(t, "package p\n\nimport \"testing\"\n\nfunc TestP(t *testing.T) { t.Fatal(\"boom\") }\n")
		code, out, errw := run(t, "", "time", "--json")
		if code != 1 {
			t.Fatalf("code %d, errw %q, out %q", code, errw, out)
		}
		var got struct {
			OK     bool `json:"ok"`
			Checks []struct {
				Exit int    `json:"exit"`
				Tail string `json:"tail"`
			} `json:"checks"`
		}
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if got.OK || len(got.Checks) != 1 || got.Checks[0].Exit != 1 || !strings.Contains(got.Checks[0].Tail, "boom") {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("2 on two paths", func(t *testing.T) {
		if code, _, _ := run(t, "", "time", "a", "b"); code != 2 {
			t.Errorf("code %d", code)
		}
	})
}
