// Package golang extracts Go tests (func TestX(t *testing.T), including
// t.Run subtests) into cull TestCases. It ports the declaration indexing,
// callee walk and context rules of the Phase 0 extractor
// (cull-calibration/extractors/goext) and adds byte-exact spans and subtest
// handling.
package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/schuettc/tackle/internal/cull/cases"
	"github.com/schuettc/tackle/internal/cull/extract"
)

func init() {
	extract.Register(New)
}

// defaultMaxContext is the budget used when the caller passes 0.
const defaultMaxContext = cases.DefaultMaxContextBytes

type golangExtractor struct{}

// New builds the Go extractor.
func New() extract.Extractor { return golangExtractor{} }

func (golangExtractor) Lang() string { return "go" }

// Match reports whether relpath is a Go test file outside testdata/ and
// vendor/.
func (golangExtractor) Match(relpath string) bool {
	relpath = filepath.ToSlash(relpath)
	if !strings.HasSuffix(relpath, "_test.go") {
		return false
	}
	for _, part := range strings.Split(relpath, "/") {
		if part == "testdata" || part == "vendor" {
			return false
		}
	}
	return true
}

// decl is a named top-level declaration: its source and the file it's in.
type decl struct {
	name, file, src string
}

// pkgDecls maps top-level names to their source for .go files (or _test.go
// files, if tests is true) directly in dir. Ported verbatim from goext.
func pkgDecls(root, dir string, tests bool) map[string]decl {
	out := map[string]decl{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		n := e.Name()
		if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") != tests {
			continue
		}
		p := filepath.Join(dir, n)
		src, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, p, src, parser.ParseComments)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		text := func(nd ast.Node) string {
			return string(src[fs.Position(nd.Pos()).Offset:fs.Position(nd.End()).Offset])
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				key := d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					key = "." + d.Name.Name // methods: matched by selector name
				}
				start := d.Pos()
				if d.Doc != nil {
					start = d.Doc.Pos()
				}
				out[key] = decl{d.Name.Name, rel, string(src[fs.Position(start).Offset:fs.Position(d.End()).Offset])}
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						out[s.Name.Name] = decl{s.Name.Name, rel, text(d)}
					case *ast.ValueSpec:
						for _, nm := range s.Names {
							out[nm.Name] = decl{nm.Name, rel, text(d)}
						}
					}
				}
			}
		}
	}
	return out
}

// module is the Go module a test file belongs to: its directory and path.
type module struct {
	dir, path string
	err       error
}

// findModule returns the module owning dir: the nearest go.mod at or above
// dir, never looking above root. No go.mod is an error (the file is
// skipped, not fatal), as is a go.mod that can't be read or has no module
// line. Results are cached per directory.
func (st *extractState) findModule(dir string) module {
	if m, ok := st.modCache[dir]; ok {
		return m
	}
	var m module
	root := filepath.Clean(st.root)
	for d := dir; ; {
		gomod := filepath.Join(d, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			path, err := modulePath(d)
			m = module{dir: d, path: path, err: err}
			break
		}
		parent := filepath.Dir(d)
		if d == root || parent == d {
			m = module{err: fmt.Errorf("no go.mod")}
			break
		}
		d = parent
	}
	st.modCache[dir] = m
	return m
}

// modulePath reads the module path from dir/go.mod.
func modulePath(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module")), nil
		}
	}
	return "", fmt.Errorf("go.mod: no module line")
}

// extractState is the shared, per-Extract-call context the walk needs.
type extractState struct {
	root       string
	modCache   map[string]module // test file dir -> its module
	maxContext int
	idCount    map[string]int
	pkgCache   map[string]map[string]decl // import dir -> its prod decls
}

// Extract implements extract.Extractor.
func (golangExtractor) Extract(root string, relpaths []string, maxContext int) (extract.Result, error) {
	if maxContext <= 0 {
		maxContext = defaultMaxContext
	}
	st := &extractState{
		root:       root,
		modCache:   map[string]module{},
		maxContext: maxContext,
		idCount:    map[string]int{},
		pkgCache:   map[string]map[string]decl{},
	}

	var res extract.Result
	dirTestDecls := map[string]map[string]decl{}
	dirProdDecls := map[string]map[string]decl{}

	for _, rel := range relpaths {
		relSlash := filepath.ToSlash(rel)
		p := filepath.Join(root, filepath.FromSlash(rel))
		src, err := os.ReadFile(p)
		if err != nil {
			res.Skipped = append(res.Skipped, extract.Skipped{File: relSlash, Reason: fmt.Sprintf("read: %v", err)})
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, src, parser.ParseComments)
		if err != nil {
			res.Skipped = append(res.Skipped, extract.Skipped{File: relSlash, Reason: fmt.Sprintf("parse: %v", err)})
			continue
		}

		dir := filepath.Dir(p)
		mod := st.findModule(dir)
		if mod.err != nil {
			res.Skipped = append(res.Skipped, extract.Skipped{File: relSlash, Reason: mod.err.Error()})
			continue
		}
		testDecls, ok := dirTestDecls[dir]
		if !ok {
			testDecls = pkgDecls(root, dir, true)
			dirTestDecls[dir] = testDecls
		}
		prodDecls, ok := dirProdDecls[dir]
		if !ok {
			prodDecls = pkgDecls(root, dir, false)
			dirProdDecls[dir] = prodDecls
		}

		imports := map[string]string{}
		for _, im := range f.Imports {
			path := strings.Trim(im.Path.Value, `"`)
			if !strings.HasPrefix(path, mod.path+"/") {
				continue
			}
			name := filepath.Base(path)
			if im.Name != nil {
				name = im.Name.Name
			}
			imports[name] = filepath.Join(mod.dir, strings.TrimPrefix(path, mod.path+"/"))
		}

		fw := &fileWalk{
			state:     st,
			fset:      fset,
			src:       src,
			relFile:   relSlash,
			testDecls: testDecls,
			prodDecls: prodDecls,
			imports:   imports,
		}

		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			fw.walkTest(fn, &res)
		}
	}
	return res, nil
}

// fileWalk holds the per-file inputs to the goext-style callee/context walk.
type fileWalk struct {
	state     *extractState
	fset      *token.FileSet
	src       []byte
	relFile   string
	testDecls map[string]decl
	prodDecls map[string]decl
	imports   map[string]string
}

// runCall is a direct t.Run(name, func(t *testing.T) { ... }) call found in
// a block: not nested inside another such call's closure.
type runCall struct {
	call *ast.CallExpr
	lit  *ast.FuncLit
}

// isRunCall reports whether call looks like t.Run(name, func(t *testing.T) { ... }).
func isRunCall(call *ast.CallExpr) (*ast.FuncLit, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Run" || len(call.Args) != 2 {
		return nil, false
	}
	lit, ok := call.Args[1].(*ast.FuncLit)
	if !ok || lit.Type.Params == nil || len(lit.Type.Params.List) != 1 {
		return nil, false
	}
	star, ok := lit.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return nil, false
	}
	switch te := star.X.(type) {
	case *ast.Ident:
		if te.Name != "T" {
			return nil, false
		}
	case *ast.SelectorExpr:
		if te.Sel.Name != "T" {
			return nil, false
		}
	default:
		return nil, false
	}
	return lit, true
}

// directRuns finds t.Run calls directly in body, not descending into the
// closures of t.Run calls it finds (those belong to a deeper recursion).
func directRuns(body ast.Node) []runCall {
	var out []runCall
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if lit, ok := isRunCall(call); ok {
			out = append(out, runCall{call: call, lit: lit})
			return false
		}
		return true
	})
	return out
}

// subtestName derives the name component go test would give a t.Run call:
// the literal string with spaces replaced by underscores, or
// "[<expr source>]" for a name computed at runtime.
func (fw *fileWalk) subtestName(nameExpr ast.Expr) string {
	if lit, ok := nameExpr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		if s, err := strconv.Unquote(lit.Value); err == nil {
			return strings.ReplaceAll(s, " ", "_")
		}
	}
	start := fw.fset.Position(nameExpr.Pos()).Offset
	end := fw.fset.Position(nameExpr.End()).Offset
	return "[" + string(fw.src[start:end]) + "]"
}

// assignID applies the "id", "id #2", "id #3" dedup rule to an emitted
// TestCase's base id.
func (st *extractState) assignID(base string) string {
	st.idCount[base]++
	n := st.idCount[base]
	if n == 1 {
		return base
	}
	return fmt.Sprintf("%s #%d", base, n)
}

// walkContext runs the goext callee/context walk over body: same-file
// test-file decls referenced become Context (sorted, deduped), same-package
// or in-module-imported-package production decls referenced become
// Callees, in call order, deduped. selfName (if non-empty) is excluded from
// Context so a top-level test doesn't reference its own declaration.
// initialSize seeds the size budget so callers that prepend bytes outside
// this walk (e.g. a subtest's enclosing setup text) count those bytes
// against the same maxContext budget: if initialSize alone already exceeds
// maxContext, truncated is true even when nothing else is added.
func (fw *fileWalk) walkContext(body ast.Node, selfName string, initialSize int) (context string, callees []cases.Callee, truncated bool) {
	var ctxParts []string
	seenCtx, seenCallee := map[string]bool{}, map[string]bool{}
	size := initialSize
	truncated = size > fw.state.maxContext
	add := func(d decl, isCtx bool, sym string) {
		if size+len(d.src) > fw.state.maxContext {
			truncated = true
			return
		}
		size += len(d.src)
		if isCtx {
			ctxParts = append(ctxParts, d.src)
		} else {
			callees = append(callees, cases.Callee{Symbol: sym, File: d.file, Source: d.src})
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if d, ok := fw.testDecls[x.Name]; ok && !seenCtx[x.Name] && x.Name != selfName {
				seenCtx[x.Name] = true
				add(d, true, x.Name)
			}
		case *ast.CallExpr:
			switch fnx := x.Fun.(type) {
			case *ast.Ident:
				if d, ok := fw.prodDecls[fnx.Name]; ok && !seenCallee[fnx.Name] {
					seenCallee[fnx.Name] = true
					add(d, false, fnx.Name)
				}
			case *ast.SelectorExpr:
				if id, ok := fnx.X.(*ast.Ident); ok {
					if pdir, ok := fw.imports[id.Name]; ok {
						if fw.state.pkgCache[pdir] == nil {
							fw.state.pkgCache[pdir] = pkgDecls(fw.state.root, pdir, false)
						}
						sym := id.Name + "." + fnx.Sel.Name
						if d, ok := fw.state.pkgCache[pdir][fnx.Sel.Name]; ok && !seenCallee[sym] {
							seenCallee[sym] = true
							add(d, false, sym)
						}
						return true
					}
				}
				if d, ok := fw.prodDecls["."+fnx.Sel.Name]; ok && !seenCallee["."+fnx.Sel.Name] {
					seenCallee["."+fnx.Sel.Name] = true
					add(d, false, fnx.Sel.Name)
				}
			}
		}
		return true
	})
	sort.Strings(ctxParts)
	return strings.Join(ctxParts, "\n\n"), callees, truncated
}

// walkTest processes one top-level Test function: either a leaf (no
// subtests, emitted with the parity-matching goext body/context/callees)
// or a container whose direct t.Run children are recursed into.
func (fw *fileWalk) walkTest(fn *ast.FuncDecl, res *extract.Result) {
	baseID := fmt.Sprintf("go:%s:%s", fw.relFile, fn.Name.Name)
	runs := directRuns(fn.Body)
	if len(runs) == 0 {
		start := fn.Pos()
		if fn.Doc != nil {
			start = fn.Doc.Pos()
		}
		startOff := fw.fset.Position(start).Offset
		endOff := fw.fset.Position(fn.End()).Offset
		body := string(fw.src[startOff:endOff])
		ctx, callees, trunc := fw.walkContext(fn.Body, fn.Name.Name, 0)
		res.Cases = append(res.Cases, cases.TestCase{
			ID:        fw.state.assignID(baseID),
			Hash:      cases.HashBody(body),
			Lang:      "go",
			Framework: "testing",
			File:      fw.relFile,
			Name:      fn.Name.Name,
			Body:      body,
			Context:   ctx,
			Span:      cases.Span{Start: startOff, End: endOff},
			Callees:   callees,
			Truncated: trunc,
		})
		return
	}
	bodyStart := fw.fset.Position(fn.Body.Lbrace).Offset + 1
	for _, r := range runs {
		name := fw.subtestName(r.call.Args[0])
		setup := string(fw.src[bodyStart:fw.fset.Position(r.call.Pos()).Offset])
		fw.walkSubtest(r.lit, baseID, name, setup, res)
	}
}

// walkSubtest processes one t.Run closure: either a leaf (emitted, with
// context = the enclosing setup source plus same-file helpers it uses) or a
// container whose own direct t.Run children are recursed into.
func (fw *fileWalk) walkSubtest(lit *ast.FuncLit, parentID, name, setup string, res *extract.Result) {
	id := parentID + "/" + name
	runs := directRuns(lit.Body)
	if len(runs) == 0 {
		startOff := fw.fset.Position(lit.Pos()).Offset
		endOff := fw.fset.Position(lit.End()).Offset
		body := string(fw.src[startOff:endOff])
		ctx, callees, trunc := fw.walkContext(lit.Body, "", len(setup))
		full := setup
		if ctx != "" {
			if full != "" {
				full += "\n\n"
			}
			full += ctx
		}
		res.Cases = append(res.Cases, cases.TestCase{
			ID:        fw.state.assignID(id),
			Hash:      cases.HashBody(body),
			Lang:      "go",
			Framework: "testing",
			File:      fw.relFile,
			Name:      name,
			Parent:    parentID,
			Body:      body,
			Context:   full,
			Span:      cases.Span{Start: startOff, End: endOff},
			Callees:   callees,
			Truncated: trunc,
		})
		return
	}
	bodyStart := fw.fset.Position(lit.Body.Lbrace).Offset + 1
	for _, r := range runs {
		childName := fw.subtestName(r.call.Args[0])
		childSetup := string(fw.src[bodyStart:fw.fset.Position(r.call.Pos()).Offset])
		fw.walkSubtest(r.lit, id, childName, childSetup, res)
	}
}
