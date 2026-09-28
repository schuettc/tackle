// Package pkgb is a test fixture for TestWireGeneratorRejectsDuplicateNames.
// It provides a type named Clash that intentionally shares its simple Go name
// with pkga.Clash to trigger the wire generator's duplicate-name check.
package pkgb

// Clash is a fixture type: same simple name as pkga.Clash, different package.
type Clash struct {
	B string `json:"b"`
}
