package apply

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// forceGoTidyFallback, set by tests, skips the goimports-on-PATH lookup
// so both TidyGo code paths (goimports binary, go/ast fallback) can be
// exercised regardless of what's installed on the machine running tests.
var forceGoTidyFallback bool

// TidyGo removes now-unused imports from a Go file and gofmts the
// result. It prefers `goimports` from PATH (goimports -w semantics);
// absent that, it falls back to a go/ast pass that drops any imported
// name never used as a selector qualifier (X.Sel) elsewhere in the file,
// leaving `_` and `.` imports untouched. It returns the removed imports'
// paths.
func TidyGo(src []byte) ([]byte, []string, error) {
	if !forceGoTidyFallback {
		if p, err := exec.LookPath("goimports"); err == nil {
			return tidyGoImports(p, src)
		}
	}
	return tidyGoFallback(src)
}

// goTidyFallsBack reports whether TidyGo uses the go/ast fallback (no
// goimports on PATH), which keeps imports whose package name it can't
// know; apply then hints at installing goimports when the tests fail.
func goTidyFallsBack() bool {
	if forceGoTidyFallback {
		return true
	}
	_, err := exec.LookPath("goimports")
	return err != nil
}

func tidyGoImports(binPath string, src []byte) ([]byte, []string, error) {
	cmd := exec.Command(binPath)
	cmd.Env = envWithoutKey()
	cmd.Stdin = bytes.NewReader(src)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("goimports: %w: %s", err, errBuf.String())
	}
	removed, err := removedImportPaths(src, out.Bytes())
	if err != nil {
		return nil, nil, err
	}
	return out.Bytes(), removed, nil
}

// envWithoutKey is the current environment minus TYPESAFE_API_KEY: the
// key never reaches a helper subprocess such as goimports.
func envWithoutKey() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TYPESAFE_API_KEY=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func removedImportPaths(before, after []byte) ([]string, error) {
	b, err := importPaths(before)
	if err != nil {
		return nil, err
	}
	a, err := importPaths(after)
	if err != nil {
		return nil, err
	}
	inAfter := make(map[string]bool, len(a))
	for _, p := range a {
		inAfter[p] = true
	}
	var removed []string
	for _, p := range b {
		if !inAfter[p] {
			removed = append(removed, p)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func importPaths(src []byte) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		paths = append(paths, p)
	}
	return paths, nil
}

var majorVersionSuffix = regexp.MustCompile(`^v[0-9]+$`)

// importLocalName is the identifier code in the file uses to refer to an
// import: its explicit name if any, else the last path element (skipping
// a trailing /vN major-version suffix, e.g. ".../foo/v2" -> "foo").
func importLocalName(imp *ast.ImportSpec) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	p, _ := strconv.Unquote(imp.Path.Value)
	parts := strings.Split(p, "/")
	name := parts[len(parts)-1]
	if len(parts) > 1 && majorVersionSuffix.MatchString(name) {
		name = parts[len(parts)-2]
	}
	return name
}

// usedQualifiers collects every identifier used as the qualifier (X) of
// a selector expression (X.Sel) anywhere in the file.
func usedQualifiers(f *ast.File) map[string]bool {
	used := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				used[id.Name] = true
			}
		}
		return true
	})
	return used
}

func tidyGoFallback(src []byte) ([]byte, []string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}

	used := usedQualifiers(f)

	toRemove := map[*ast.ImportSpec]bool{}
	var removed []string
	for _, imp := range f.Imports {
		name := importLocalName(imp)
		if name == "_" || name == "." {
			continue
		}
		// Unaliased, the package name is only known when the last path
		// element is a plain identifier ("yaml.v3", "go-colorable" are
		// not): keep the import; verify catches it if it really is unused.
		if imp.Name == nil && !token.IsIdentifier(name) {
			continue
		}
		if !used[name] {
			p, _ := strconv.Unquote(imp.Path.Value)
			removed = append(removed, p)
			toRemove[imp] = true
		}
	}

	if len(toRemove) == 0 {
		out, err := format.Source(src)
		if err != nil {
			return nil, nil, err
		}
		return out, nil, nil
	}

	var decls []ast.Decl
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			decls = append(decls, decl)
			continue
		}
		var specs []ast.Spec
		for _, s := range gd.Specs {
			if is, ok := s.(*ast.ImportSpec); ok && toRemove[is] {
				continue
			}
			specs = append(specs, s)
		}
		if len(specs) == 0 {
			continue
		}
		gd.Specs = specs
		decls = append(decls, gd)
	}
	f.Decls = decls

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return nil, nil, err
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(removed)
	return out, removed, nil
}
