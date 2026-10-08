package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteChecksShowsSetupLines(t *testing.T) {
	root := t.TempDir()
	wf := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wf, "ci.yml"), []byte("jobs:\n  a:\n    steps:\n      - run: npm ci && npx jest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	writeChecks(&buf, root)
	if !strings.Contains(buf.String(), "jest  .  npx jest\n    setup: npm ci\n") {
		t.Errorf("output = %q", buf.String())
	}
}
