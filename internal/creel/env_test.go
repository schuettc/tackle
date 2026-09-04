package creel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpsertAddsToNewFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")
	action, err := Upsert(dest, "OPENAI_API_KEY", "sk-abc123")
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if action != Added {
		t.Fatalf("action = %q, want %q", action, Added)
	}
	got := readFile(t, dest)
	if got != "OPENAI_API_KEY=sk-abc123\n" {
		t.Fatalf("contents = %q", got)
	}
	if mode := statMode(t, dest); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
}

func TestUpsertReplacesExistingKeyInPlace(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")
	writeFile(t, dest, "A=1\nOPENAI_API_KEY=old\nB=2\n")

	action, err := Upsert(dest, "OPENAI_API_KEY", "new")
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if action != Updated {
		t.Fatalf("action = %q, want %q", action, Updated)
	}
	got := readFile(t, dest)
	if got != "A=1\nOPENAI_API_KEY=new\nB=2\n" {
		t.Fatalf("contents = %q — other keys must be preserved and order kept", got)
	}
}

func TestUpsertAppendsWhenKeyAbsent(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")
	writeFile(t, dest, "A=1\n")

	if _, err := Upsert(dest, "B", "2"); err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	got := readFile(t, dest)
	if got != "A=1\nB=2\n" {
		t.Fatalf("contents = %q", got)
	}
}

func TestUpsertNoDuplicateOnRepeat(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")
	if _, err := Upsert(dest, "K", "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := Upsert(dest, "K", "2"); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, dest)
	if got != "K=2\n" {
		t.Fatalf("contents = %q — repeated upsert must not duplicate the line", got)
	}
}

func TestUpsertValueWithEquals(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")
	if _, err := Upsert(dest, "TOKEN", "a=b=c"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, dest); got != "TOKEN=a=b=c\n" {
		t.Fatalf("contents = %q", got)
	}
}

func TestHasKey(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")

	if ok, _ := HasKey(dest, "X"); ok {
		t.Fatal("missing file should report no key")
	}
	writeFile(t, dest, "X=1\nXY=2\n")
	if ok, _ := HasKey(dest, "X"); !ok {
		t.Fatal("X should be present")
	}
	if ok, _ := HasKey(dest, "Z"); ok {
		t.Fatal("Z should be absent")
	}
	if ok, _ := HasKey(dest, "XY"); !ok {
		t.Fatal("XY should be present (prefix must include the =)")
	}
}

func TestResolveDest(t *testing.T) {
	cwd := t.TempDir()

	got, err := ResolveDest(cwd, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(cwd, ".env") {
		t.Fatalf("default = %q", got)
	}

	got, err = ResolveDest(cwd, "config/.env")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(cwd, "config/.env") {
		t.Fatalf("relative = %q", got)
	}

	if _, err := ResolveDest(cwd, "../escape.env"); err == nil {
		t.Fatal("dest escaping cwd must be rejected")
	}
	if _, err := ResolveDest(cwd, "/etc/passwd"); err == nil {
		t.Fatal("absolute dest outside cwd must be rejected")
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"A", "OPENAI_API_KEY", "_x", "a1_b2"}
	invalid := []string{"", "1ABC", "A-B", "A B", "A.B", "$X"}
	for _, s := range valid {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true, want false", s)
		}
	}
}

func TestEnsureGitignored(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".env")

	// No .gitignore: no-op, none created.
	if err := EnsureGitignored(dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("EnsureGitignored must not create a .gitignore")
	}

	// Existing .gitignore lacking .env: appended.
	gi := filepath.Join(dir, ".gitignore")
	writeFile(t, gi, "node_modules\n")
	if err := EnsureGitignored(dest); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, gi); got != "node_modules\n.env\n" {
		t.Fatalf("gitignore = %q", got)
	}

	// Idempotent: already present, unchanged.
	if err := EnsureGitignored(dest); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, gi); got != "node_modules\n.env\n" {
		t.Fatalf("gitignore changed on repeat = %q", got)
	}
}

func TestDetect(t *testing.T) {
	cases := map[string]string{
		"sk-ant-abc":     "looks like an Anthropic key",
		"sk-proj-abc":    "looks like an OpenAI key",
		"ghp_abc":        "looks like a GitHub token",
		"AKIAIOSFODNN7":  "looks like an AWS access key",
		"xoxb-1-2":       "looks like a Slack token",
		"AIzaSyabc":      "looks like a Google API key",
		"totally-random": "",
	}
	for in, want := range cases {
		if got := Detect(in); got != want {
			t.Errorf("Detect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWriteStatus(t *testing.T) {
	if err := WriteStatus("", "added"); err != nil {
		t.Fatalf("empty path should be a no-op, got %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "status")
	if err := WriteStatus(p, "updated"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p); got != "updated\n" {
		t.Fatalf("status = %q", got)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func statMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}
