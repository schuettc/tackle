package proj

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrNoRoots = errors.New("no project roots configured")

type Roots struct {
	Roots    []string // dirs whose children are projects
	Projects []string // dirs that are themselves projects
}

func rootsPath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "proj", "roots")
	}
	return filepath.Join(os.Getenv("HOME"), ".config", "proj", "roots")
}

// expand applies leading ~ then $VAR expansion, strips a trailing slash.
func expand(s string) string {
	if strings.HasPrefix(s, "~") {
		s = filepath.Join(os.Getenv("HOME"), strings.TrimPrefix(s, "~"))
	}
	s = os.ExpandEnv(s)
	if s != "/" {
		s = strings.TrimRight(s, "/")
	}
	return s
}

func LoadRoots() (Roots, error) {
	f, err := os.Open(rootsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return Roots{}, ErrNoRoots
		}
		return Roots{}, err
	}
	defer f.Close()
	var r Roots
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		isProject := false
		if strings.HasPrefix(line, "project:") {
			isProject = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "project:"))
		}
		p := expand(line)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		if isProject {
			r.Projects = append(r.Projects, p)
		} else {
			r.Roots = append(r.Roots, p)
		}
	}
	if len(r.Roots)+len(r.Projects) == 0 {
		return r, ErrNoRoots
	}
	return r, sc.Err()
}

func (r Roots) NameForDir(dir string) (string, bool) {
	for _, p := range r.Projects { // declared projects are the more specific claim
		if dir == p || strings.HasPrefix(dir, p+string(os.PathSeparator)) {
			return filepath.Base(p), true
		}
	}
	for _, root := range r.Roots {
		prefix := root + string(os.PathSeparator)
		if strings.HasPrefix(dir, prefix) {
			rel := strings.TrimPrefix(dir, prefix)
			return strings.SplitN(rel, string(os.PathSeparator), 2)[0], true
		}
	}
	return "", false
}

func (r Roots) DirForName(name string) (string, bool) {
	for _, p := range r.Projects {
		if filepath.Base(p) == name {
			return p, true
		}
	}
	for _, root := range r.Roots {
		cand := filepath.Join(root, name)
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			return cand, true
		}
	}
	return "", false
}

func (r Roots) AllProjectDirs() []string {
	var out []string
	for _, root := range r.Roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				out = append(out, filepath.Join(root, e.Name()))
			}
		}
	}
	out = append(out, r.Projects...)
	return out
}
