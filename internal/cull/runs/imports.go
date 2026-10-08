package runs

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var importRes = []*regexp.Regexp{
	regexp.MustCompile(`(?:import|export)\s[^'"]*?from\s*['"](\.[^'"]*)['"]`),
	regexp.MustCompile(`import\s*['"](\.[^'"]*)['"]`),
	regexp.MustCompile(`(?:require|import)\s*\(\s*['"](\.[^'"]*)['"]\s*\)`),
}

var jsExts = []string{".mjs", ".js", ".cjs", ".ts", ".mts", ".cts", ".tsx", ".jsx"}

func isJS(p string) bool {
	switch path.Ext(p) {
	case ".mjs", ".js", ".cjs", ".ts", ".mts", ".cts", ".tsx", ".jsx":
		return true
	}
	return false
}

// localImports returns entry and every project file it imports through
// relative specifiers, transitively, root-relative and sorted. Nothing is
// returned when entry does not exist.
func (e *engine) localImports(entry string) []string {
	if !e.exists(entry) {
		return nil
	}
	seen := map[string]bool{}
	var walk func(f string)
	walk = func(f string) {
		if seen[f] {
			return
		}
		seen[f] = true
		if !isJS(f) {
			return
		}
		b, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(f)))
		if err != nil {
			return
		}
		for _, re := range importRes {
			for _, m := range re.FindAllStringSubmatch(string(b), -1) {
				if r := e.resolveImport(path.Dir(f), m[1]); r != "" {
					walk(r)
				}
			}
		}
	}
	walk(entry)
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func (e *engine) resolveImport(dir, spec string) string {
	cand := join(dir, spec)
	if cand == ".." || strings.HasPrefix(cand, "../") || strings.Contains(cand, "node_modules/") {
		return ""
	}
	tries := []string{cand}
	if ext := path.Ext(cand); ext == ".js" || ext == ".mjs" || ext == ".cjs" {
		stem := strings.TrimSuffix(cand, ext)
		tries = append(tries, stem+".ts", stem+".mts", stem+".cts", stem+".tsx")
	}
	for _, x := range jsExts {
		tries = append(tries, cand+x)
	}
	for _, x := range jsExts {
		tries = append(tries, path.Join(cand, "index"+x))
	}
	for _, t := range tries {
		if e.exists(t) {
			return t
		}
	}
	return ""
}
