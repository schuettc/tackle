package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := Main(args, strings.NewReader(stdin), &out, &errw)
	return code, out.String(), errw.String()
}

// The command index lists exactly what sift implements today: the
// framework's built-ins plus sift's own commands.
func TestCommandsJSONListsWhatIsImplemented(t *testing.T) {
	code, out, errw := run(t, "", "commands", "--json")
	if code != 0 {
		t.Fatalf("code %d: %s", code, errw)
	}
	var cmds []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &cmds); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range cmds {
		got = append(got, c.Name)
	}
	sort.Strings(got)
	want := []string{"check", "commands", "doctor", "help", "init", "man", "rows", "update", "version"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands %v, want %v", got, want)
	}
}
