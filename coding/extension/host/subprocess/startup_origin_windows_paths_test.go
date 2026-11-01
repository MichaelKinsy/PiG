package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Pi 1be312b4 (#10488) resource-loader.ts loadFinalExtensionSet matches a preloaded extension to a final path by getResourcePathKey: on Windows paths that differ only in casing are one
// extension, and a path the project_trust pass attempted is not loaded again.
func TestFinalSetOriginIgnoresPathCasingOnWindows(t *testing.T) {
	loaded := ExtConfig{Name: "a", Path: "/Work/Ext/Index.ts"}
	final := ExtConfig{Name: "a", Path: "/work/ext/index.ts"}
	if finalSetOrigin("/cwd", loaded) == finalSetOrigin("/cwd", final) {
		t.Fatal("paths in other casing matched on a case-sensitive platform")
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if finalSetOrigin("/cwd", loaded) != finalSetOrigin("/cwd", final) {
		t.Fatalf("origins %q and %q differ on Windows", finalSetOrigin("/cwd", loaded), finalSetOrigin("/cwd", final))
	}
	if got := finalSetOrigin("/cwd", ExtConfig{Name: "fused-one"}); got != "fused:fused-one" {
		t.Fatalf("origin of a config with no path = %q", got)
	}
}
