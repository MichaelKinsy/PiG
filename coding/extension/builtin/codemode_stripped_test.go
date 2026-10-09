package builtin_test

import (
	"testing"
)

// docs/specs/builtin-codemode-tool-search.md, "Strip": a build with the `pig_strip_codemode` tag links neither the sandbox,
// its wasm module nor wazero; the untagged build links all of them.
func TestStripCodemodeBuildOmitsCodemodeAndItsEngine(t *testing.T) {
	engine := []string{"github.com/tetratelabs/wazero", "github.com/MichaelKinsy/PiG/codemode", "github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"}
	stock := goListDeps(t, "", pigModule+"/cmd/pig")
	stripped := goListDeps(t, "pig_strip_codemode", pigModule+"/cmd/pig")
	for _, pkg := range engine {
		if !linksPackage(stock, pkg) {
			t.Errorf("the stock cmd/pig does not link %s", pkg)
		}
		if linksPackage(stripped, pkg) {
			t.Errorf("a pig_strip_codemode build of cmd/pig links %s", pkg)
		}
	}
}
