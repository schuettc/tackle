package timing

import (
	"strings"
	"testing"
)

func TestPytestNote(t *testing.T) {
	const xdist = "pytest-xdist isn't installed in venv: CI installs it; install the project's dev dependencies"
	cases := []struct {
		name string
		argv []string
		out  string
		want string
	}{
		{"xdist", []string{"venv/bin/pytest", "-n", "8"}, "ERROR: usage: pytest ...\npytest: error: unrecognized arguments: -n 8\n", xdist},
		{"numprocesses=", []string{"venv/bin/pytest", "--numprocesses=4"}, "pytest: error: unrecognized arguments: --numprocesses=4", xdist},
		{"dist", []string{"venv/bin/pytest", "--dist", "loadfile"}, "pytest: error: unrecognized arguments: --dist loadfile", xdist},
		{"cov", []string{".venv/bin/python", "-m", "pytest", "--cov=app"}, "pytest: error: unrecognized arguments: --cov=app", "pytest-cov isn't installed in .venv: CI installs it; install the project's dev dependencies"},
		{"timeout", []string{"venv/bin/pytest", "--timeout=5"}, "pytest: error: unrecognized arguments: --timeout=5", "pytest-timeout isn't installed in venv: CI installs it; install the project's dev dependencies"},
		{"reruns", []string{"venv/bin/pytest", "--reruns", "2"}, "pytest: error: unrecognized arguments: --reruns 2", "pytest-rerunfailures isn't installed in venv: CI installs it; install the project's dev dependencies"},
		{"two", []string{"venv/bin/pytest", "-n", "2", "--cov"}, "pytest: error: unrecognized arguments: -n 2 --cov", "pytest-xdist and pytest-cov aren't installed in venv: CI installs them; install the project's dev dependencies"},
		{"unknown", []string{"venv/bin/pytest", "--frobnicate"}, "pytest: error: unrecognized arguments: --frobnicate", "pytest doesn't recognise --frobnicate: a plugin CI installs is probably missing here"},
		{"no env dir", []string{"pytest", "-n", "8"}, "pytest: error: unrecognized arguments: -n 8", "pytest-xdist isn't installed in this Python environment: CI installs it; install the project's dev dependencies"},
		{"other failure", []string{"venv/bin/pytest", "-n", "8"}, "1 failed", ""},
	}
	for _, c := range cases {
		if got := pytestNote(c.argv, c.out); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWriteTextPrintsNote(t *testing.T) {
	var b strings.Builder
	WriteText(&b, &Report{Checks: []CheckResult{{Kind: "pytest", Dir: ".", Exit: 4, Tail: "x", Note: "pytest-xdist isn't installed in venv"}}})
	if !strings.Contains(b.String(), "  note: pytest-xdist isn't installed in venv\n") {
		t.Errorf("no note:\n%s", b.String())
	}
}
