package apply

import (
	"strings"
	"testing"
)

func TestTidyGoKeepsUsedImports(t *testing.T) {
	src := `package a

import (
	"fmt"
	"os"
)

func run() {
	fmt.Println(os.Args)
}
`
	for _, forced := range []bool{false, true} {
		t.Run(withFallback(forced), func(t *testing.T) {
			forceGoTidyFallback = forced
			defer func() { forceGoTidyFallback = false }()

			out, removed, err := TidyGo([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if len(removed) != 0 {
				t.Errorf("removed = %v, want none", removed)
			}
			if !strings.Contains(string(out), `"fmt"`) || !strings.Contains(string(out), `"os"`) {
				t.Errorf("output dropped a used import:\n%s", out)
			}
		})
	}
}

func TestTidyGoDropsUnused(t *testing.T) {
	src := `package a

import (
	"fmt"
	"os"
	"strings"
)

func run() {
	fmt.Println(os.Args)
}
`
	for _, forced := range []bool{false, true} {
		t.Run(withFallback(forced), func(t *testing.T) {
			forceGoTidyFallback = forced
			defer func() { forceGoTidyFallback = false }()

			out, removed, err := TidyGo([]byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if len(removed) != 1 || removed[0] != "strings" {
				t.Fatalf("removed = %v, want [strings]", removed)
			}
			if strings.Contains(string(out), `"strings"`) {
				t.Errorf("output still imports strings:\n%s", out)
			}
			if !strings.Contains(string(out), `"fmt"`) || !strings.Contains(string(out), `"os"`) {
				t.Errorf("output dropped a used import:\n%s", out)
			}
		})
	}
}

func TestTidyGoKeepsBlankAndDotImports(t *testing.T) {
	src := `package a

import (
	_ "net/http/pprof"
	. "fmt"
	"strings"
)

func run() {
	Println("x")
}
`
	forceGoTidyFallback = true
	defer func() { forceGoTidyFallback = false }()

	out, removed, err := TidyGo([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "strings" {
		t.Fatalf("removed = %v, want [strings]", removed)
	}
	if !strings.Contains(string(out), `_ "net/http/pprof"`) {
		t.Errorf("output dropped the blank import:\n%s", out)
	}
	if !strings.Contains(string(out), `. "fmt"`) {
		t.Errorf("output dropped the dot import:\n%s", out)
	}
}

func withFallback(forced bool) string {
	if forced {
		return "fallback"
	}
	return "goimports-or-fallback"
}
