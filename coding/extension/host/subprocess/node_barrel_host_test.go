//go:build !pig_strip_node_extensions

package subprocess

// pi: packages/coding-agent/src/index.ts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// An extension loaded by the Go host imports "@earendil-works/pi-coding-agent" and sees exactly the runtime value exports of Pi's published SDK entry point
// (packages/coding-agent/src/index.ts), plus the barrel's own `__runtime`. The barrel is delivered into the runtime tree by materializeNodeRuntime, so this
// proves the host serves it, where TestNodeBarrelExportsEveryValuePiExports compares the barrel file in isolation.
func TestNodeHostServesTheSDKBarrelPiExports(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	published := filepath.Join(pioracle.Root(t), "..", "..", "..", "..", "..", "..", "..", "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist", "index.js")
	var names struct{ Published []string }
	pioracle.Run(t, `
const { pathToFileURL } = await import("node:url");
const published = await import(pathToFileURL(input.published).href);
emit({ Published: Object.keys(published).sort() });`, map[string]string{"published": filepath.Clean(published)}, &names)
	if len(names.Published) < 100 {
		t.Fatalf("the published entry point exports %d names; the comparison reads the wrong module", len(names.Published))
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "barrel-exports.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	entry := filepath.Join(root, "barrel-exports.mjs")
	if err := os.WriteFile(entry, fixture, 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(root)
	t.Cleanup(func() { host.Shutdown("test complete") })
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{{Name: "barrel-exports", Source: entry, Enabled: true, Isolation: "strict"}})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("load barrel-exports: extensions=%d errors=%v", len(loaded), errs)
	}
	command, ok := loaded[0].Commands["barrel-exports"]
	if !ok {
		t.Fatal("barrel-exports command was not registered")
	}
	args, err := json.Marshal(map[string][]string{"published": names.Published})
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Handler(t.Context(), string(args)); err != nil {
		t.Fatalf("the barrel the host serves differs from Pi's exports: %v", err)
	}
}
