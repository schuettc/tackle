// Registry order is deterministic: golang, python, ts. Import all three
// language packages here (for side effect, via their init funcs calling
// Register) so a single test exercises the combined registry that
// whatever wires cull's CLI together will also produce.
package extract_test

import (
	"testing"

	"github.com/schuettc/tackle/internal/cull/extract"
	_ "github.com/schuettc/tackle/internal/cull/extract/golang"
	_ "github.com/schuettc/tackle/internal/cull/extract/python"
	_ "github.com/schuettc/tackle/internal/cull/extract/ts"
)

func TestRegistryOrder(t *testing.T) {
	var got []string
	for _, e := range extract.Registry() {
		got = append(got, e.Lang())
	}
	want := []string{"go", "python", "typescript"}
	if len(got) != len(want) {
		t.Fatalf("Registry() langs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Registry()[%d].Lang() = %q, want %q (order must be golang, python, ts)", i, got[i], want[i])
		}
	}
}
