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
// Declarations: Go top-level funcs (not methods), vars, consts, types
// (go/ast); Python column-0 def/async def/class and NAME = (so pytest
// fixtures count: they are matched by parameter name); TypeScript
// column-0 function/class/const/let/var, optionally exported. The same
// word-boundary rule applies to all: mentioned in a removed body, and at
// most its own declaration's mention left in after. Advisory only.
func OrphanedHelpers(before, after []byte, lang string, removedBodies []string) []string {
	if len(removedBodies) == 0 {
		return nil
	}
	var names []string
	selfNames := map[string]bool{}
	switch lang {
	case "go":
		names = topLevelGoNames(before)
		// A removed test's own body mentions its own name (in its func
		// signature); that's a self-reference, not a call to a helper,
		// so it must not count as "referenced in a removed test body".
		for _, body := range removedBodies {
			for n := range removedDeclNames(body) {
				selfNames[n] = true
			}
		}
	case "python", "typescript":
		names = topLevelScriptNames(lang, string(before))
		for _, body := range removedBodies {
			for _, n := range topLevelScriptNames(lang, body) {
				selfNames[n] = true
			}
		}
	default:
		return nil
	}
	if len(names) == 0 {
		return nil
	}
	removedText := strings.Join(removedBodies, "\n")

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

var (
	pyTopDecl   = regexp.MustCompile(`(?m)^(?:async[ \t]+def|def|class)[ \t]+([A-Za-z_]\w*)`)
	pyTopAssign = regexp.MustCompile(`(?m)^([A-Za-z_]\w*)[ \t]*(?::[^=\n]*)?=[^=]`)
	tsTopDecl   = regexp.MustCompile(`(?m)^(?:export[ \t]+)?(?:default[ \t]+)?(?:async[ \t]+)?(?:function\*?|class|const|let|var)[ \t]+([A-Za-z_$][\w$]*)`)
)

// topLevelScriptNames lists the column-0 declarations in a Python or
// TypeScript source (deduped, in order).
func topLevelScriptNames(lang, src string) []string {
	res := []*regexp.Regexp{tsTopDecl}
	if lang == "python" {
		res = []*regexp.Regexp{pyTopDecl, pyTopAssign}
	}
	seen := map[string]bool{}
	var names []string
	for _, re := range res {
		for _, m := range re.FindAllStringSubmatch(src, -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				names = append(names, m[1])
			}
		}
	}
	return names
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
