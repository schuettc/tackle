package apply

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
)

// OrphanedHelpers reports (never deletes) same-file top-level
// declarations that a removed test's body referenced and that nothing in
// after (the file post-edit) references any more. before is the file's
// content prior to editing (so declarations still visible there can be
// found even though the test that used them is now gone from after);
// removedBodies are the source text of the spans RemoveSpans just took
// out.
//
// Currently implemented for Go (lang == "go"); other languages report no
// orphans.
func OrphanedHelpers(before, after []byte, lang string, removedBodies []string) []string {
	if lang != "go" || len(removedBodies) == 0 {
		return nil
	}
	names := topLevelGoNames(before)
	if len(names) == 0 {
		return nil
	}
	removedText := strings.Join(removedBodies, "\n")

	// A removed test's own body mentions its own name (in its func
	// signature); that's a self-reference, not a call to a helper, so
	// it must not count as "referenced in a removed test body".
	selfNames := map[string]bool{}
	for _, body := range removedBodies {
		for n := range removedDeclNames(body) {
			selfNames[n] = true
		}
	}

	var orphans []string
	for _, name := range names {
		if selfNames[name] {
			continue
		}
		if !wordRegexp(name).MatchString(removedText) {
			continue
		}
		// The declaration itself still mentions its own name once (its
		// signature/spec); more than that means something still calls or
		// references it.
		if wordCount(after, name) <= 1 {
			orphans = append(orphans, name)
		}
	}
	sort.Strings(orphans)
	return orphans
}

func wordRegexp(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
}

func wordCount(src []byte, name string) int {
	return len(wordRegexp(name).FindAll(src, -1))
}

// removedDeclNames parses one removed span's source (expected to be a
// single top-level Go declaration, e.g. a test func) and returns the
// name(s) it declares, so OrphanedHelpers can tell a test's reference to
// its own name apart from a call to a helper.
func removedDeclNames(body string) map[string]bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p\n\n"+body, 0)
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, n := range declNames(f) {
		set[n] = true
	}
	return set
}

// topLevelGoNames lists every top-level function (excluding methods),
// var, const and type name declared in src.
func topLevelGoNames(src []byte) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", src, 0)
	if err != nil {
		return nil
	}
	return declNames(f)
}

func declNames(f *ast.File) []string {
	var names []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.ValueSpec:
					for _, id := range s.Names {
						if id.Name != "_" {
							names = append(names, id.Name)
						}
					}
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				}
			}
		}
	}
	return names
}
