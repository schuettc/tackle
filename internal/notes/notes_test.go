package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Path's derivation is covered in locate_test.go against an injected
// environment. This only pins the exported entry point: it honors the
// explicit override and never returns a path inside the working directory.
func TestPath(t *testing.T) {
	t.Setenv(EnvFile, "/tmp/pinned-pad.md")
	got, err := Path("/tmp/work")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if got != "/tmp/pinned-pad.md" {
		t.Fatalf("Path() = %q, want the pinned file", got)
	}

	t.Setenv(EnvFile, "")
	t.Setenv(EnvDir, t.TempDir())
	got, err = Path("/tmp/work")
	if err != nil {
		t.Fatalf("Path() error = %v", err)
	}
	if strings.HasPrefix(got, "/tmp/work") {
		t.Fatalf("Path() = %q, want it outside the working directory", got)
	}
}

func TestReadMissingIsEmpty(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	got, err := Read(p)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got != "" {
		t.Fatalf("Read() = %q, want empty", got)
	}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".scratch.md")
	const content = "# notes\n\n- one\n- two\n"
	if err := Write(p, content); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := Read(p)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got != content {
		t.Fatalf("round-trip = %q, want %q", got, content)
	}
	// No temp files left behind after a successful write.
	tmps, _ := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	if len(tmps) != 0 {
		t.Fatalf("leftover temp files: %v", tmps)
	}
}

func TestWriteRenameFailureKeepsTemp(t *testing.T) {
	dir := t.TempDir()
	// Make the target path itself an existing directory so os.Rename onto
	// it fails, forcing the rename-failure branch.
	target := filepath.Join(dir, ".scratch.md")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Write(target, "content")
	if err == nil {
		t.Fatal("Write() to a directory path should fail on rename")
	}
	// The temp file must be kept (not cleaned up) so no content is lost.
	tmps, _ := filepath.Glob(filepath.Join(dir, ".*.tmp"))
	if len(tmps) != 1 {
		t.Fatalf("rename failure should keep exactly 1 temp file, found %d: %v", len(tmps), tmps)
	}
	// The kept temp file's path must appear in the error message.
	if !strings.Contains(err.Error(), tmps[0]) {
		t.Fatalf("error %q should name the kept temp file %q", err.Error(), tmps[0])
	}
}

func TestAppendToMissingCreates(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	if err := Append(p, "first"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	got, _ := Read(p)
	if got != "first\n" {
		t.Fatalf("Append() = %q, want %q", got, "first\n")
	}
}

func TestAppendAddsSeparatingNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".scratch.md")
	if err := Write(p, "existing"); err != nil { // no trailing newline
		t.Fatalf("Write() error = %v", err)
	}
	if err := Append(p, "next"); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	got, _ := Read(p)
	if got != "existing\nnext\n" {
		t.Fatalf("Append() = %q, want %q", got, "existing\nnext\n")
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name       string
		disk, last string
		dirty      bool
		want       Action
	}{
		{"self-write echo ignored", "abc", "abc", false, Ignore},
		{"self-write echo ignored even if dirty", "abc", "abc", true, Ignore},
		{"external change while clean reloads", "new", "old", false, Reload},
		{"external change while dirty flags", "new", "old", true, Flag},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.disk, c.last, c.dirty); got != c.want {
				t.Fatalf("Classify(%q,%q,%v) = %v, want %v",
					c.disk, c.last, c.dirty, got, c.want)
			}
		})
	}
}
