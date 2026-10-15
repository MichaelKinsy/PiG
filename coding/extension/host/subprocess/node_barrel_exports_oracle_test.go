package subprocess

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// TestNodeBarrelExportsEveryValuePiExports compares the runtime value exports of the Node barrel (shims/pi-coding-agent.mjs, which ports packages/coding-agent/src/index.ts) with those of the published Pi 1.0.4 package entry point: a name Pi exports and the barrel lacks fails an extension that imports it, and the barrel adds only `__runtime`.
func TestNodeBarrelExportsEveryValuePiExports(t *testing.T) {
	published := filepath.Join(pioracle.Root(t), "..", "..", "..", "..", "..", "..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "index.js")
	var got struct {
		Barrel, Published []string
	}
	pioracle.Run(t, `
const { pathToFileURL } = await import("node:url");
const barrel = await import(pathToFileURL(root + "/../pi-coding-agent.mjs").href);
const published = await import(pathToFileURL(input.published).href);
emit({ Barrel: Object.keys(barrel).sort(), Published: Object.keys(published).sort() });`, map[string]string{"published": filepath.Clean(published)}, &got)
	if len(got.Published) < 100 {
		t.Fatalf("the published entry point exports %d names; the comparison reads the wrong module", len(got.Published))
	}
	for _, name := range got.Published {
		if !slices.Contains(got.Barrel, name) {
			t.Errorf("the Node barrel does not export %q", name)
		}
	}
	for _, name := range got.Barrel {
		if !slices.Contains(got.Published, name) && name != "__runtime" {
			t.Errorf("the Node barrel exports %q, which Pi does not", name)
		}
	}
}
