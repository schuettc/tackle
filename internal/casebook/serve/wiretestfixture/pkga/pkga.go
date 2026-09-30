// Package pkga is a test fixture for TestWireGeneratorRejectsDuplicateNames.
// It provides a type named Clash that intentionally shares its simple Go name
// with pkgb.Clash to trigger the wire generator's duplicate-name check.
package pkga

// Clash is a fixture type: same simple name as pkgb.Clash, different package.
type Clash struct {
	A string `json:"a"`
}
