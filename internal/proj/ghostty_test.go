package proj

import (
	"errors"
	"slices"
	"testing"
)

// fakeOsascript swaps the osascript seam for one returning out/err, and
// records every script it is handed.
func fakeOsascript(t *testing.T, out string, err error) *[]string {
	t.Helper()
	var scripts []string
	orig := osascript
	t.Cleanup(func() { osascript = orig })
	osascript = func(script string, args ...string) (string, error) {
		scripts = append(scripts, script)
		return out, err
	}
	return &scripts
}

func TestTitleSession(t *testing.T) {
	cases := map[string]string{
		"fde-platform/plugins · plugins": "fde-platform/plugins",
		"🔔 a/b · x":                      "a/b",
		"🔐 a/b":                          "a/b",
		"extensions-workspace/output":    "extensions-workspace/output",
		"@alias · topic":                 "@alias",
	}
	for in, want := range cases {
		if got := titleSession(in); got != want {
			t.Errorf("titleSession(%q) = %q want %q", in, got, want)
		}
	}
}

func TestMatchLayout(t *testing.T) {
	titles := [][]string{
		{"tools-workspace/creel-message · creel-message", "schuettc/reboot · reboot"},
		{"~"},
		{"caffeinate -d -i"},
		{"@alias · x", "a/b"},
	}
	live := map[string]bool{"tools-workspace/creel-message": true, "schuettc/reboot": true, "a/b": true}
	got := MatchLayout(titles, live)
	want := [][]string{{"tools-workspace/creel-message", "schuettc/reboot"}, {"a/b"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("MatchLayout = %q want %q", got, want)
	}
}

func TestParseGhosttyOutput(t *testing.T) {
	got := parseGhosttyTitles("W\nT\ta · x\nT\tb\nW\nT\tc\n")
	want := [][]string{{"a · x", "b"}, {"c"}}
	if !slices.EqualFunc(got, want, slices.Equal[[]string]) {
		t.Fatalf("parse = %q want %q", got, want)
	}
}

func TestSaveLayoutWritesRecord(t *testing.T) {
	recordHome(t)
	fakeOsascript(t, "W\nT\ta/1 · one\nT\tb/1\nW\nT\tc/1\nW\nT\t~\n", nil)
	live := map[string]bool{"a/1": true, "b/1": true, "c/1": true}
	w, n, err := SaveLayout(live)
	if err != nil || w != 2 || n != 3 {
		t.Fatalf("SaveLayout = (%d, %d, %v) want (2, 3, nil)", w, n, err)
	}
	rec, _ := LoadRecord()
	want := [][]string{{"a/1", "b/1"}, {"c/1"}}
	if !slices.EqualFunc(rec.Layout.Windows, want, slices.Equal[[]string]) || rec.Layout.SavedAt.IsZero() {
		t.Fatalf("layout = %+v", rec.Layout)
	}

	fakeOsascript(t, "W\nT\tc/1\n", nil)
	if _, _, err := SaveLayout(live); err != nil {
		t.Fatal(err)
	}
	rec, _ = LoadRecord()
	if !slices.EqualFunc(rec.Layout.Windows, [][]string{{"c/1"}}, slices.Equal[[]string]) {
		t.Fatalf("second save did not replace: %q", rec.Layout.Windows)
	}
}

func TestSaveLayoutErrorLeavesRecord(t *testing.T) {
	recordHome(t)
	fakeOsascript(t, "W\nT\ta/1\n", nil)
	if _, _, err := SaveLayout(map[string]bool{"a/1": true}); err != nil {
		t.Fatal(err)
	}
	fakeOsascript(t, "", errors.New("Ghostty got an error"))
	if _, _, err := SaveLayout(map[string]bool{"a/1": true}); err == nil {
		t.Fatal("SaveLayout swallowed the osascript error")
	}
	rec, _ := LoadRecord()
	if !slices.EqualFunc(rec.Layout.Windows, [][]string{{"a/1"}}, slices.Equal[[]string]) {
		t.Fatalf("failed save changed the layout: %q", rec.Layout.Windows)
	}
}
