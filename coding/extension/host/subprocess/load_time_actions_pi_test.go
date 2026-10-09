package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestNodeLoadTimeActionsMatchPiLoader runs one factory that calls every action method of the pinned Pi's ExtensionAPI while it loads
// (core/extensions/loader.ts createExtensionRuntime), through Pi's own loadExtensionFromFactory and through the PiG Node runtime, and
// compares the outcome of every call: what it throws, rejects or returns.
func TestNodeLoadTimeActionsMatchPiLoader(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required for the load-time action fixture: %v", err)
	}
	modRoot := findModuleRoot(t)
	dist := filepath.Join(modRoot, "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "dist")
	if _, err := os.Stat(dist); err != nil {
		t.Fatalf("pinned Pi package missing (run npm ci in extensions/sdk-ts): %v", err)
	}
	factory, err := filepath.Abs(filepath.Join("testdata", "load_time_actions_factory.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	script := `import { pathToFileURL } from "node:url";
const [dist, factoryPath] = process.argv.slice(1);
const { loadExtensionFromFactory, createExtensionRuntime } = await import(pathToFileURL(dist + "/core/extensions/loader.js").href);
const { createEventBus } = await import(pathToFileURL(dist + "/core/event-bus.js").href);
const factory = (await import(pathToFileURL(factoryPath).href)).default;
const ext = await loadExtensionFromFactory(factory, process.cwd(), createEventBus(), createExtensionRuntime(), "<inline:1>");
console.log([...ext.commands.values()][0].description);
process.exit(0);`
	piOut, err := exec.Command(node, "--input-type=module", "--eval", script, dist, factory).Output()
	if err != nil {
		t.Fatalf("Pi loader: %v", err)
	}
	cwd := t.TempDir()
	host := NewHost(cwd)
	defer host.Shutdown("test done")
	// No bridge binds exec while a factory loads: the host runs it in its working directory, as Pi's loader does.
	host.SetUIBridge(NewUIBridge(func() {}))
	ext, err := host.Load(testbudget.Context(t), ExtConfig{Name: "load-time-actions-pi", Source: factory, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	got := ext.Commands["probe"].Description
	if want := strings.TrimSpace(string(piOut)); got != want {
		t.Errorf("load-time action outcomes differ\n pig: %s\n  pi: %s", got, want)
	}
}
