package apply

import (
	"os/exec"
	"strings"
	"testing"
)

func TestTidyDispatchesGo(t *testing.T) {
	forceGoTidyFallback = true
	defer func() { forceGoTidyFallback = false }()

	src := `package a

import (
	"fmt"
	"os"
)

func run() {
	fmt.Println("hi")
}
`
	out, removed, err := Tidy(t.TempDir(), "a.go", "go", nil, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "os" {
		t.Fatalf("removed = %v, want [os]", removed)
	}
	if strings.Contains(string(out), `"os"`) {
		t.Errorf("output still imports os:\n%s", out)
	}
}

func TestTidyDispatchesPython(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found in PATH, skipping")
	}
	src := "import os\n\n\ndef test_x():\n    assert True\n"
	out, removed, err := Tidy(t.TempDir(), "tests/test_x.py", "python", []byte(src+"\n\ndef test_gone():\n    assert os\n"), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := "\n\ndef test_x():\n    assert True\n"
	if string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
	if len(removed) != 1 || removed[0] != "os" {
		t.Errorf("removed = %v, want [os]", removed)
	}
}

func TestTidyUnknownLangUnchanged(t *testing.T) {
	src := []byte("some source\n")
	out, removed, err := Tidy(t.TempDir(), "f.rb", "ruby", src, src)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(src) {
		t.Errorf("out = %q, want unchanged %q", out, src)
	}
	if removed != nil {
		t.Errorf("removed = %v, want nil", removed)
	}
}
