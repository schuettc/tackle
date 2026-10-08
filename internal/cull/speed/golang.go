package speed

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var goUnits = map[string]float64{
	"Nanosecond": 1e-9, "Microsecond": 1e-6, "Millisecond": 1e-3,
	"Second": 1, "Minute": 60, "Hour": 3600,
}

type goKind int

const (
	gkBad goKind = iota
	gkNum
	gkUnit
)

// goDuration evaluates a duration expression made of literals and time
// units. kind gkNum is a bare number, gkUnit a duration in seconds.
func goDuration(e ast.Expr) (float64, goKind) {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return goDuration(x.X)
	case *ast.BasicLit:
		if x.Kind == token.INT || x.Kind == token.FLOAT {
			s := strings.ReplaceAll(x.Value, "_", "")
			if v, err := strconv.ParseFloat(s, 64); err == nil {
				return v, gkNum
			}
			if v, err := strconv.ParseInt(s, 0, 64); err == nil {
				return float64(v), gkNum
			}
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Name == "time" {
			if u, ok := goUnits[x.Sel.Name]; ok {
				return u, gkUnit
			}
		}
	case *ast.CallExpr: // time.Duration(300)
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && len(x.Args) == 1 {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "time" && sel.Sel.Name == "Duration" {
				if v, k := goDuration(x.Args[0]); k == gkNum {
					return v, gkNum
				}
			}
		}
	case *ast.BinaryExpr:
		a, ak := goDuration(x.X)
		b, bk := goDuration(x.Y)
		if ak == gkBad || bk == gkBad {
			return 0, gkBad
		}
		switch x.Op {
		case token.MUL:
			switch {
			case ak == gkNum && bk == gkNum:
				return a * b, gkNum
			case ak == gkNum && bk == gkUnit:
				return a * b, gkUnit
			case ak == gkUnit && bk == gkNum:
				return a * b, gkUnit
			}
		case token.ADD:
			if ak == bk {
				return a + b, ak
			}
		}
	}
	return 0, gkBad
}

func goKeeper(fset *token.FileSet, f *ast.File, src []byte) keeper {
	lines := strings.Split(string(src), "\n")
	k := keeper{marker: map[int]bool{}}
	k.lineBlank = func(n int) bool {
		if n < 1 || n > len(lines) {
			return false
		}
		t := strings.TrimSpace(lines[n-1])
		return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*")
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			k.addComment(fset.Position(c.Pos()).Line, c.Text)
		}
	}
	return k
}

func scanGoWaits(rel string, src []byte) []Finding {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	k := goKeeper(fset, f, src)
	var out []Finding
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sleep" {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "time" {
			return true
		}
		pos := fset.Position(call.Pos())
		if k.keeps(pos.Line) {
			return true
		}
		fd := Finding{Kind: KindFixedWait, File: rel, Line: pos.Line, offset: pos.Offset, Detail: "time.Sleep"}
		if v, kind := goDuration(call.Args[0]); kind == gkUnit {
			fd.Seconds = secs(v)
			fd.Detail = fmt.Sprintf("time.Sleep of %gs", v)
		} else if kind == gkNum {
			fd.Seconds = secs(v * 1e-9)
		}
		out = append(out, fd)
		return true
	})
	return out
}

// Setenv helpers.

type goFn struct {
	dir    string
	file   string
	name   string
	isTest bool
	isMeth bool
	sites  []int // lines of direct t.Setenv calls
	calls  []goCall
	keep   map[int]bool
}

type goCall struct {
	pkg  string // selector qualifier, "" for a plain call
	name string
}

type goProject struct {
	fns     []*goFn
	byName  map[string][]*goFn // dir + "\x00" + name, plain functions
	methods map[string][]*goFn // method name
	pkgDirs map[string][]string
	testsOf map[string][]*goFn // dir -> test functions
}

func scanGoSetenv(root string, listed map[string]bool) []Finding {
	p := &goProject{byName: map[string][]*goFn{}, methods: map[string][]*goFn{}, pkgDirs: map[string][]string{}, testsOf: map[string][]*goFn{}}
	seenPkg := map[string]bool{}
	var goFiles []string
	_ = filepath.WalkDir(root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry is skipped
		}
		name := d.Name()
		if d.IsDir() {
			if abs != root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") {
			if rel, err := filepath.Rel(root, abs); err == nil {
				goFiles = append(goFiles, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	for _, rel := range goFiles {
		name := path.Base(rel)
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		dir := path.Dir(rel)
		pkg := strings.TrimSuffix(f.Name.Name, "_test")
		if !seenPkg[dir+"\x00"+pkg] {
			seenPkg[dir+"\x00"+pkg] = true
			p.pkgDirs[pkg] = append(p.pkgDirs[pkg], dir)
		}
		k := goKeeper(fset, f, src)
		isTestFile := strings.HasSuffix(name, "_test.go")
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			fn := &goFn{dir: dir, file: rel, name: fd.Name.Name, isMeth: fd.Recv != nil, keep: map[int]bool{}}
			fn.isTest = isTestFile && !fn.isMeth && strings.HasPrefix(fn.name, "Test") && hasTestingParam(fd)
			shadow := paramNames(fd)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					fn.calls = append(fn.calls, goCall{name: fun.Name})
				case *ast.SelectorExpr:
					id, isID := fun.X.(*ast.Ident)
					if fun.Sel.Name == "Setenv" && isID && id.Name != "os" && id.Name != "syscall" {
						pos := fset.Position(call.Pos())
						if !k.keeps(pos.Line) {
							fn.sites = append(fn.sites, pos.Line)
						}
						return true
					}
					if isID && !shadow[id.Name] {
						fn.calls = append(fn.calls, goCall{pkg: id.Name, name: fun.Sel.Name})
					} else {
						fn.calls = append(fn.calls, goCall{pkg: "?", name: fun.Sel.Name})
					}
				}
				return true
			})
			p.fns = append(p.fns, fn)
			if fn.isMeth {
				p.methods[fn.name] = append(p.methods[fn.name], fn)
			} else {
				p.byName[dir+"\x00"+fn.name] = append(p.byName[dir+"\x00"+fn.name], fn)
			}
			if fn.isTest {
				p.testsOf[dir] = append(p.testsOf[dir], fn)
			}
		}
	}

	type siteKey struct {
		file string
		line int
	}
	type siteHit struct {
		fn      *goFn
		reached int
		total   int
		dir     string
		line    int
	}
	best := map[siteKey]*siteHit{}
	dirs := make([]string, 0, len(p.testsOf))
	for d := range p.testsOf {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	listedDirs := map[string]bool{}
	for f := range listed {
		if strings.HasSuffix(f, "_test.go") {
			listedDirs[path.Dir(f)] = true
		}
	}
	for _, dir := range dirs {
		if !listedDirs[dir] {
			continue // only packages whose test files were asked for
		}
		tests := p.testsOf[dir]
		reachCount := map[*goFn]int{} // setter function -> tests reaching it
		reaching := 0
		for _, t := range tests {
			seen := map[*goFn]bool{}
			p.walk(t, seen)
			any := false
			for fn := range seen {
				if len(fn.sites) > 0 {
					reachCount[fn]++
					any = true
				}
			}
			if any {
				reaching++
			}
		}
		if reaching < minSetenvTests || reaching*setenvShareDeno < len(tests) {
			continue
		}
		fns := make([]*goFn, 0, len(reachCount))
		for fn := range reachCount {
			fns = append(fns, fn)
		}
		sort.Slice(fns, func(i, j int) bool {
			if fns[i].file != fns[j].file {
				return fns[i].file < fns[j].file
			}
			return fns[i].sites[0] < fns[j].sites[0]
		})
		directReported := false
		for _, fn := range fns {
			if fn.isTest {
				if directReported {
					continue
				}
				directReported = true
			}
			// One finding per helper, at its first t.Setenv.
			line := fn.sites[0]
			key := siteKey{fn.file, line}
			if fn.isTest {
				key = siteKey{dir + "\x00direct", 0}
			}
			h := &siteHit{fn: fn, reached: reaching, total: len(tests), dir: dir, line: line}
			if cur, ok := best[key]; !ok || h.reached > cur.reached {
				best[key] = h
			}
		}
	}
	var out []Finding
	for _, h := range best {
		var detail string
		if h.fn.isTest {
			detail = fmt.Sprintf("t.Setenv in %d of %d tests in %s: they cannot run with t.Parallel()", h.reached, h.total, h.dir)
		} else {
			detail = fmt.Sprintf("t.Setenv in %s, reached by %d of %d tests in %s: they cannot run with t.Parallel()", h.fn.name, h.reached, h.total, h.dir)
		}
		out = append(out, Finding{Kind: KindSetenv, File: h.fn.file, Line: h.line, Detail: detail})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func hasTestingParam(fd *ast.FuncDecl) bool {
	if fd.Type.Params == nil || len(fd.Type.Params.List) != 1 {
		return false
	}
	star, ok := fd.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}

func paramNames(fd *ast.FuncDecl) map[string]bool {
	m := map[string]bool{}
	if fd.Recv != nil {
		for _, f := range fd.Recv.List {
			for _, n := range f.Names {
				m[n.Name] = true
			}
		}
	}
	if fd.Type.Params != nil {
		for _, f := range fd.Type.Params.List {
			for _, n := range f.Names {
				m[n.Name] = true
			}
		}
	}
	return m
}

// walk marks fn and every function it reaches through calls.
func (p *goProject) walk(fn *goFn, seen map[*goFn]bool) {
	if seen[fn] {
		return
	}
	seen[fn] = true
	for _, c := range fn.calls {
		for _, callee := range p.resolve(fn, c) {
			p.walk(callee, seen)
		}
	}
}

func (p *goProject) resolve(from *goFn, c goCall) []*goFn {
	switch c.pkg {
	case "":
		return p.byName[from.dir+"\x00"+c.name]
	case "?":
		return p.methodsNamed(from, c.name)
	}
	if dirs, ok := p.pkgDirs[c.pkg]; ok {
		var out []*goFn
		for _, d := range dirs {
			out = append(out, p.byName[d+"\x00"+c.name]...)
		}
		if len(out) > 0 {
			return out
		}
		return nil
	}
	return p.methodsNamed(from, c.name)
}

func (p *goProject) methodsNamed(from *goFn, name string) []*goFn {
	ms := p.methods[name]
	var same []*goFn
	for _, m := range ms {
		if m.dir == from.dir {
			same = append(same, m)
		}
	}
	if len(same) > 0 {
		return same
	}
	if len(ms) == 1 {
		return ms
	}
	return nil
}
