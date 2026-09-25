package piglet

import (
	"go/build"
	"slices"
	"testing"
)

// TestPigletPackageDoesNotImportProductMarketplace locks the decoupling:
// the generic Piglet package must resolve contributed typed sources through
// product-neutral registries, never a concrete product package.
func TestPigletPackageDoesNotImportProductMarketplace(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("import piglet package: %v", err)
	}
	const banned = "github.com/MichaelKinsy/PiG/piglets/standard"
	imports := slices.Concat(pkg.Imports, pkg.TestImports, pkg.XTestImports)
	if slices.Contains(imports, banned) {
		t.Fatalf("piglet package imports the product marketplace package %q; route through installresolver instead", banned)
	}
}
