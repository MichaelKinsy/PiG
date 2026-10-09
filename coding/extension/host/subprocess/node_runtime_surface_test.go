//go:build !pig_strip_node_extensions

package subprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Pi hands every extension one event bus (resource-loader.ts): an event one
// extension emits reaches the others, pi.events.on() returns the unsubscribe
// function, emit() returns nothing, a throwing handler is reported as Pi
// reports it, and subscriptions a failed factory made are dropped
// (loader.ts createExtensionAPI). pi.on() returns an unsubscribe function
// too, and registerMarkdownTransformer exists (pi-goal-x,
// @henryqw/pi-task-models, pi-cc-extensions).
func TestNodeRuntimeEventBusMatchesPi(t *testing.T) {
	nodeCellRequireNode(t)
	runtimePath, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
const { Runtime } = await import(pathToFileURL(%q));
const reported = [];
console.error = (...args) => reported.push(args.map(String).join(" "));
const a = new Runtime("a.mjs"), b = new Runtime("b.mjs"), failed = new Runtime("failed.mjs");
const heard = [];
const off = a.api.events.on("probe", (n) => heard.push("a" + n));
b.api.events.on("probe", (n) => heard.push("b" + n));
failed.api.events.on("probe", (n) => heard.push("failed" + n));
b.api.events.on("boom", () => { throw new Error("handler boom"); });
a.commitLoad();
b.commitLoad();
failed.discardLoad();
assert.equal(typeof off, "function");
assert.equal(b.api.events.emit("probe", 1), undefined);
off();
b.api.events.emit("probe", 2);
a.api.events.emit("boom", 0);
await new Promise(setImmediate);
assert.deepEqual(heard, ["a1", "b1", "b2"]);
assert.ok(reported.some((line) => line.startsWith("Event handler error (boom): Error: handler boom")), reported.join("\n"));
const removeHandler = a.api.on("session_start", () => {});
assert.equal(typeof removeHandler, "function");
assert.equal(typeof a.api.registerMarkdownTransformer, "function");
`, runtimePath)
	cmd := exec.CommandContext(testbudget.Context(t), "node", "--input-type=module", "--eval", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("event bus: %v\n%s", err, out)
	}
}

// A Node extension whose module or factory throws is reported with Pi's
// loader message (loader.ts loadExtension), carried from the extension's own
// loader rather than scraped from its stderr, where Node's
// stripTypeScriptTypes warning used to stand in for it. A healthy member of
// the same process still loads.
func TestNodeLoadFailureReportsPiLoaderMessage(t *testing.T) {
	nodeCellRequireNode(t)
	dir := t.TempDir()
	write := func(name, source string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	throws := write("throws.ts", "import type { ExtensionAPI } from \"@earendil-works/pi-coding-agent\";\nexport default function (pi: ExtensionAPI) {\n  (pi as any).notAThing();\n}\n")
	noFactory := write("no-factory.mjs", "export const value = 1;\n")
	healthy := write("healthy.mjs", "export default function (pi) { pi.registerCommand(\"ok\", { description: \"ok\", handler: async () => {} }); }\n")
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	loaded, errs := h.LoadAll(t.Context(), []ExtConfig{
		{Name: "throws", Source: throws, Enabled: true},
		{Name: "no-factory", Source: noFactory, Enabled: true},
		{Name: "healthy", Source: healthy, Enabled: true},
	})
	if len(loaded) != 1 || loaded[0].Name != "healthy" {
		t.Fatalf("loaded = %v, want only healthy", loaded)
	}
	want := map[string]string{
		"throws":     "Failed to load extension: pi.notAThing is not a function",
		"no-factory": "Extension does not export a valid factory function: " + noFactory,
	}
	if len(errs) != len(want) {
		t.Fatalf("errs = %v, want one per failed extension", errs)
	}
	for _, err := range errs {
		loadErr, ok := errors.AsType[*ExtensionLoadError](err)
		if !ok {
			t.Fatalf("error %v is not an ExtensionLoadError", err)
		}
		factoryErr, ok := errors.AsType[*FactoryLoadError](err)
		if !ok {
			t.Fatalf("%s: error %q does not carry the extension's loader error", loadErr.Name, err)
		}
		if factoryErr.Message != want[loadErr.Name] || loadErr.Err.Error() != want[loadErr.Name] {
			t.Errorf("%s: loader error %q (reported as %q), want %q", loadErr.Name, factoryErr.Message, loadErr.Err, want[loadErr.Name])
		}
		if strings.Contains(err.Error(), "trace-warnings") {
			t.Errorf("%s: error %q names Node's warning", loadErr.Name, err)
		}
	}
}
