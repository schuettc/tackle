package apply

import (
	"os"
	"path/filepath"
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

// TestTidyGoImportsEnvHasNoKey runs a fake goimports that records its
// environment: TYPESAFE_API_KEY must not reach it (M-1).
func TestTidyGoImportsEnvHasNoKey(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "sk-secret-test")
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	bin := filepath.Join(dir, "goimports")
	script := "#!/bin/sh\nenv > '" + envFile + "'\ncat\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	src := "package a\n"
	if _, _, err := tidyGoImports(bin, []byte(src)); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(env), "TYPESAFE_API_KEY") {
		t.Fatalf("goimports got TYPESAFE_API_KEY in its environment")
	}
	if !strings.Contains(string(env), "PATH=") {
		t.Fatalf("goimports env lost PATH:\n%s", env)
	}
}
