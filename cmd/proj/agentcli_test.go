package main

import "testing"

func TestParseNewArg(t *testing.T) {
	p, w, err := parseNewTarget("tools-workspace/nfl cutover")
	if err != nil {
		t.Fatal(err)
	}
	if p != "tools-workspace" || w != "nfl-cutover" {
		t.Fatalf("%q %q", p, w)
	}
	if _, _, err := parseNewTarget("noslash"); err == nil {
		t.Fatal("want error on missing /")
	}
	if _, _, err := parseNewTarget("p/bad.name"); err == nil {
		t.Fatal("want error on invalid work")
	}
}
