package runs

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func itoa(i int) string { return strconv.Itoa(i) }

type pkgInfo struct {
	dir     string // root-relative
	scripts map[string]string
}

// pkgFor finds the nearest package.json at or above dir, within the root.
func (e *engine) pkgFor(dir string) *pkgInfo {
	for d := dir; ; d = path.Dir(d) {
		if strings.Contains(d, "{{") || d == ".." || strings.HasPrefix(d, "../") {
			return nil
		}
		if p, ok := e.pkgs[d]; ok {
			if p != nil {
				return p
			}
		} else {
			var p *pkgInfo
			if b, err := os.ReadFile(filepath.Join(e.root, filepath.FromSlash(d), "package.json")); err == nil {
				var v struct {
					Scripts map[string]string `json:"scripts"`
				}
				if json.Unmarshal(b, &v) == nil {
					p = &pkgInfo{dir: d, scripts: v.Scripts}
				}
			}
			e.pkgs[d] = p
			if p != nil {
				return p
			}
		}
		if d == "." || d == "/" || d == "" {
			return nil
		}
	}
}

var npmScriptVerbs = map[string]bool{"run": true, "run-script": true, "rum": true, "urn": true}
var npmTestVerbs = map[string]bool{"test": true, "t": true, "tst": true}

var plainDir = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_./-]*$`)

// dirFlag moves c.dir for a flag that names a directory: --prefix and -C say
// where the package is; a workspace or filter value counts only when it is a
// plain relative directory that exists. It returns a note when it cannot.
func (e *engine) dirFlag(dir *string, flag, val string) string {
	if strings.ContainsAny(val, "$`~{}*?!") || strings.HasPrefix(val, "/") {
		return flag + " " + val + " is not a plain directory"
	}
	switch flag {
	case "--workspace", "-w", "--filter", "-F":
		if !plainDir.MatchString(val) || strings.Contains(val, "...") || strings.HasPrefix(val, "@") {
			return flag + " " + val + " is not a plain directory"
		}
		n := join(*dir, val)
		if n == outsideDir {
			return flag + " " + val + " is outside the project"
		}
		if st, err := os.Stat(filepath.Join(e.root, filepath.FromSlash(n))); err != nil || !st.IsDir() {
			return flag + " " + val + " is not a directory in the project"
		}
		*dir = n
	default:
		*dir = join(*dir, val)
	}
	return ""
}

var npmDirFlags = map[string]bool{"--prefix": true, "-C": true, "--cwd": true, "--workspace": true, "-w": true, "--filter": true, "-F": true, "--dir": true}

// callPackageManager follows `npm run x`, `npm test`, `pnpm x`, `yarn x`.
func (e *engine) callPackageManager(argv []string, mgr string, args []string, c *ctx) {
	var rest []string
	nc := *c
	var note string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			flag, val, hasVal := strings.Cut(a, "=")
			if npmDirFlags[flag] {
				if !hasVal {
					i++
					if i < len(args) {
						val = args[i]
					}
				}
				if n := e.dirFlag(&nc.dir, flag, val); n != "" && note == "" {
					note = n
				}
			}
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		return
	}
	verb := rest[0]
	script := ""
	switch {
	case npmScriptVerbs[verb] && len(rest) > 1:
		script = rest[1]
	case npmTestVerbs[verb]:
		script = "test"
	case verb == "exec" && mgr != "npm" && len(rest) > 1:
		e.classify(argv, rest[1:], &nc)
		return
	case mgr != "npm":
		// pnpm x / yarn x: a script when the package has one, else a binary
		if p := e.pkgFor(nc.dir); p != nil && note == "" {
			if _, ok := p.scripts[verb]; ok {
				script = verb
			}
		}
		if script == "" {
			if verb == "jest" || verb == "vitest" {
				e.classify(argv, rest, &nc)
			} else if hasTestWord(verb) {
				e.emit(nc, "unknown", argv, nil, "no script "+verb+" found in a package.json")
			}
			return
		}
	default:
		return
	}
	if note != "" {
		e.emit(nc, "unknown", argv, nil, note)
		return
	}
	e.runScript(argv, script, &nc)
}

func (e *engine) runScript(argv []string, name string, c *ctx) {
	p := e.pkgFor(c.dir)
	if p == nil {
		note := "no package.json found for the directory it runs in"
		if strings.Contains(c.dir, "{{") {
			note = "the directory it runs in is not known statically"
		}
		e.emit(*c, "unknown", argv, nil, note)
		return
	}
	if _, ok := p.scripts[name]; !ok {
		e.emit(*c, "unknown", argv, nil, "no script "+name+" in "+path.Join(p.dir, "package.json"))
		return
	}
	for _, n := range []string{"pre" + name, name, "post" + name} {
		body, ok := p.scripts[n]
		if !ok {
			continue
		}
		key := "npm\x00" + p.dir + "\x00" + n
		if c.active[key] {
			continue
		}
		c.active[key] = true
		label := path.Join(p.dir, "package.json")
		hop := label + " " + n
		sc := ctx{dir: p.dir, chain: c.chain, env: c.env, vars: nil, active: c.active}
		e.runText(body, 1, func(int) string { return hop }, sc, false)
		delete(c.active, key)
	}
}
