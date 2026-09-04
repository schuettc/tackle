// Package creel is the capture core for the creel secret tool: env-var name
// validation, destination resolution, the atomic .env upsert, key-shape
// detection, and the harness status token. The value being captured only ever
// lives in this process's memory and the destination file — it is never
// returned to a calling harness, which sees only a status token.
package creel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Action is the outcome of a capture, and doubles as the harness status token.
type Action string

const (
	Added     Action = "added"
	Updated   Action = "updated"
	Cancelled Action = "cancelled"
)

// ValidName reports whether s is a legal environment-variable name
// ([A-Za-z_][A-Za-z0-9_]*). The .env line format has no quoting for the key,
// so an illegal name would produce an unparseable file.
func ValidName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		alpha := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		digit := r >= '0' && r <= '9'
		if i == 0 && !alpha {
			return false
		}
		if i > 0 && !alpha && !digit {
			return false
		}
	}
	return true
}

// ResolveDest resolves dest against cwd and rejects any path that escapes cwd.
// An empty dest defaults to ".env". The returned path is absolute and cleaned.
func ResolveDest(cwd, dest string) (string, error) {
	if dest == "" {
		dest = ".env"
	}
	p := dest
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, dest)
	}
	p = filepath.Clean(p)
	rel, err := filepath.Rel(cwd, p)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("dest-outside-cwd")
	}
	return p, nil
}

// resolveInteractive resolves a human-entered destination: ~ expansion, made
// absolute against cwd, cleaned. Unlike ResolveDest it does NOT confine the
// result to cwd — a person choosing a folder (or the folder picker) may pick
// anywhere. The cwd guard is only for the harness/agent path.
func resolveInteractive(cwd, dest string) (string, error) {
	if strings.TrimSpace(dest) == "" {
		return "", errors.New("empty destination")
	}
	if dest == "~" || strings.HasPrefix(dest, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dest = home + strings.TrimPrefix(dest, "~")
		}
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(cwd, dest)
	}
	return filepath.Clean(dest), nil
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// HasKey reports whether dest already defines name (a line beginning
// "name="). A missing file is not an error.
func HasKey(dest, name string) (bool, error) {
	b, err := os.ReadFile(dest)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	prefix := name + "="
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(ln, prefix) {
			return true, nil
		}
	}
	return false, nil
}

// Upsert sets name=value in the .env file at dest. If a "name=" line already
// exists it is replaced in place (Updated); otherwise the pair is appended
// (Added). Every other line is preserved verbatim. The write is atomic (a temp
// file in dest's directory is renamed over dest) and the file mode is 0600.
func Upsert(dest, name, value string) (Action, error) {
	b, err := os.ReadFile(dest)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}

	var lines []string
	replaced := false
	if err == nil {
		content := string(b)
		raw := strings.Split(content, "\n")
		// A trailing newline yields a final empty element; drop it so we don't
		// grow a blank line on every write. Join re-adds the terminator.
		if strings.HasSuffix(content, "\n") && len(raw) > 0 && raw[len(raw)-1] == "" {
			raw = raw[:len(raw)-1]
		}
		prefix := name + "="
		for _, ln := range raw {
			if !replaced && strings.HasPrefix(ln, prefix) {
				lines = append(lines, name+"="+value)
				replaced = true
			} else {
				lines = append(lines, ln)
			}
		}
	}

	action := Updated
	if !replaced {
		lines = append(lines, name+"="+value)
		action = Added
	}

	if err := atomicWrite(dest, strings.Join(lines, "\n")+"\n", 0o600); err != nil {
		return "", err
	}
	return action, nil
}

// EnsureGitignored appends dest's basename to a .gitignore in dest's directory
// when that .gitignore exists and does not already ignore it. It does not
// create a .gitignore where none exists — creel does not add VCS files
// uninvited.
func EnsureGitignored(dest string) error {
	dir := filepath.Dir(dest)
	base := filepath.Base(dest)
	gi := filepath.Join(dir, ".gitignore")
	b, err := os.ReadFile(gi)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == base {
			return nil
		}
	}
	content := string(b)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += base + "\n"
	return os.WriteFile(gi, []byte(content), 0o644)
}

// Detect returns a short advisory hint about a pasted secret's provenance from
// its prefix, or "" when unrecognized. It is advisory only — creel never
// rejects a key for failing to match.
func Detect(value string) string {
	switch {
	case strings.HasPrefix(value, "sk-ant-"):
		return "looks like an Anthropic key"
	case strings.HasPrefix(value, "sk-"):
		return "looks like an OpenAI key"
	case strings.HasPrefix(value, "ghp_"), strings.HasPrefix(value, "gho_"),
		strings.HasPrefix(value, "github_pat_"):
		return "looks like a GitHub token"
	case strings.HasPrefix(value, "AKIA"), strings.HasPrefix(value, "ASIA"):
		return "looks like an AWS access key"
	case strings.HasPrefix(value, "xoxb-"), strings.HasPrefix(value, "xoxp-"):
		return "looks like a Slack token"
	case strings.HasPrefix(value, "AIza"):
		return "looks like a Google API key"
	default:
		return ""
	}
}

// WriteStatus writes a single status token to path (harness mode). When path
// is a FIFO the open blocks until the reader connects, which is how the pi
// wrapper rendezvouses with a detached display-popup. An empty path is a no-op
// (standalone mode). The value is never written here — only the token.
func WriteStatus(path, token string) error {
	if path == "" {
		return nil
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func atomicWrite(path, content string, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".creel-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(e error) error {
		tmp.Close()
		os.Remove(tmpName)
		return e
	}
	if err := tmp.Chmod(mode); err != nil {
		return cleanup(err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s to %s: %w", tmpName, path, err)
	}
	return nil
}
