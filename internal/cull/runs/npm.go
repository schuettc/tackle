package runs

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
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
		if strings.Contains(d, "{{") {
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

// callPackageManager follows `npm run x`, `npm test`, `pnpm x`, `yarn x`.
func (e *engine) callPackageManager(argv []string, mgr string, args []string, c *ctx) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		if strings.HasPrefix(a, "-") {
			switch a {
			case "--prefix", "-C", "--cwd", "--workspace", "-w", "--filter":
				i++
			}
			continue
		}
		rest = append(rest, a)
	}
	if len(rest) == 0 {
		return
	}
	verb := rest[0]
	switch {
	case npmScriptVerbs[verb] && len(rest) > 1:
		e.runScript(rest[1], c)
	case npmTestVerbs[verb]:
		e.runScript("test", c)
	case verb == "exec" && mgr != "npm" && len(rest) > 1:
		e.classify(argv, rest[1:], c)
	case mgr != "npm":
		// pnpm x / yarn x: a script when the package has one, else a binary
		if p := e.pkgFor(c.dir); p != nil {
			if _, ok := p.scripts[verb]; ok {
				e.runScript(verb, c)
				return
			}
		}
		if verb == "jest" || verb == "vitest" {
			e.classify(argv, rest, c)
		}
	}
}

func (e *engine) runScript(name string, c *ctx) {
	p := e.pkgFor(c.dir)
	if p == nil {
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
		sc := ctx{dir: p.dir, chain: c.chain, vars: nil, active: c.active}
		e.runText(body, 1, func(int) string { return hop }, sc, false)
		delete(c.active, key)
	}
}
