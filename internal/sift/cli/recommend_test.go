package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// The recommending loop from a terminal: sift next gives a file and the
// guidance, sift propose stores the recommendation (one JSON object, JSON
// lines or an array), and a refusal says why with exit 2.
func TestNextAndProposeFromTheTerminal(t *testing.T) {
	home := siftEnv(t)
	fixtureWorkspace(t, home)
	if code, _, errw := run(t, "", "check"); code != 1 {
		t.Fatalf("check: %d %s", code, errw)
	}
	code, out, errw := run(t, "", "next")
	if code != 0 || !strings.Contains(out, "2 file(s) to recommend") || !strings.Contains(out, "Keep the file's own conventions") {
		t.Fatalf("next: %d\n%s\n%s", code, out, errw)
	}
	for range 2 {
		code, out, errw = run(t, "", "next", "--json")
		if code != 0 {
			t.Fatalf("next --json: %d %s", code, errw)
		}
		var n struct {
			File, Base, Content string
			Findings            []struct {
				Row     string
				Certain bool
			}
		}
		if err := json.Unmarshal([]byte(out), &n); err != nil {
			t.Fatal(err)
		}
		type acct struct{ Row, Did, How string }
		var acc []acct
		for _, f := range n.Findings {
			acc = append(acc, acct{f.Row, "fixed", "rewritten"})
		}
		body, _ := json.MarshalIndent(map[string]any{"file": n.File, "base": n.Base, "content": "# Rewritten\n\n- Keep changes small.\n",
			"findings": acc, "summary": "Rewritten as guidance."}, "", "  ")
		// A bad base first: refused, with the reason.
		bad := strings.Replace(string(body), n.Base, strings.Repeat("0", 64), 1)
		if code, _, errw := run(t, bad, "propose"); code != 2 || !strings.Contains(errw, "base") {
			t.Fatalf("bad base: %d %s", code, errw)
		}
		if code, out, errw := run(t, string(body), "propose"); code != 0 || !strings.Contains(out, "1 recommendation(s) stored") {
			t.Fatalf("propose: %d %s %s", code, out, errw)
		}
	}
	code, out, _ = run(t, "", "next")
	if code != 0 || !strings.Contains(out, "every file has a recommendation") {
		t.Fatalf("done: %d %s", code, out)
	}
}

func TestProposeRefusesUnknownFields(t *testing.T) {
	home := siftEnv(t)
	fixtureWorkspace(t, home)
	_, _, _ = run(t, "", "check")
	if code, _, errw := run(t, `{"file": "x", "contents": "y"}`, "propose"); code != 2 || !strings.Contains(errw, "contents") {
		t.Fatalf("%d %s", code, errw)
	}
	if code, _, errw := run(t, "", "propose"); code != 2 || !strings.Contains(errw, "no recommendation") {
		t.Fatalf("%d %s", code, errw)
	}
}
