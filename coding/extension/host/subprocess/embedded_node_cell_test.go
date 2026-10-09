//go:build !pig_strip_node_extensions

package subprocess

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// writeEmbeddedNodeCell lays out what a Piglet Binary extracts for one Node
// cell: one directory per member under the cell directory.
func writeEmbeddedNodeCell(t *testing.T) EmbeddedCell {
	t.Helper()
	dir := t.TempDir()
	member := filepath.Join(dir, "greeter")
	if err := os.MkdirAll(filepath.Join(member, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(member, "index.ts"):           "import { greeting } from \"./lib/greeting.ts\";\nexport default function (pi: any) {\n  pi.registerCommand(\"greet\", { description: \"greet\", handler: async () => {} });\n  pi.registerTool({ name: \"greet_tool\", label: \"Greet\", description: \"greet\", parameters: { type: \"object\", properties: {} }, execute: async () => ({ content: [{ type: \"text\", text: greeting }] }) });\n}\n",
		filepath.Join(member, "lib", "greeting.ts"): "export const greeting: string = \"embedded hello\";\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return EmbeddedCell{
		Language: "node", Key: "packed-node:embedded", Strategy: string(CellStrategyPackedNode), BinaryPath: dir,
		Extensions: []EmbeddedExtension{{Name: "greeter", Hash: "h", Source: "greeter"}},
	}
}

// A Piglet Binary's Node cell starts from its extracted sources through the
// Node cell path Stock PiG uses: the member's command and tool register, and
// its relative TypeScript import resolves inside the extracted tree.
func TestHost_LoadEmbeddedCellsStartsNodeCellFromExtractedSources(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the embedded Node cell fixture: %v", err)
	}
	cell := writeEmbeddedNodeCell(t)
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadEmbeddedCells(testbudget.Context(t), []EmbeddedCell{cell})
	if len(errs) != 0 {
		t.Fatalf("LoadEmbeddedCells errors: %v", errs)
	}
	if len(loaded) != 1 || loaded[0].Name != "greeter" {
		t.Fatalf("loaded = %#v", loaded)
	}
	if _, ok := loaded[0].Commands["greet"]; !ok {
		t.Fatalf("commands = %v, want greet", loaded[0].Commands)
	}
	tool, ok := loaded[0].Tools["greet_tool"]
	if !ok {
		t.Fatalf("tools = %v, want greet_tool", loaded[0].Tools)
	}
	result, err := tool.Definition.Execute(testbudget.Context(t), "call-1", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if text := fmt.Sprintf("%+v", result); !strings.Contains(text, "embedded hello") {
		t.Fatalf("tool result = %s, want the extracted import's value", text)
	}
	host.SetConfigLoader(func() ([]ExtConfig, error) { return nil, nil })
	reloaded, err := host.Reload(testbudget.Context(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded) != 1 || reloaded[0].Name != "greeter" {
		t.Fatalf("reloaded = %#v, want the embedded Node member", reloaded)
	}
}

// Without Node on PATH an embedded Node cell fails with the error Stock PiG
// reports for the same extension, naming the extension and the Node version it
// needs, and the failure is per extension: the host keeps running.
func TestHost_LoadEmbeddedNodeCellWithoutNodeReportsStockError(t *testing.T) {
	cell := writeEmbeddedNodeCell(t)
	t.Setenv("PATH", t.TempDir())
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadEmbeddedCells(testbudget.Context(t), []EmbeddedCell{cell})
	if len(loaded) != 0 || len(errs) != 1 {
		t.Fatalf("loaded = %#v, errors = %v", loaded, errs)
	}
	loadErr, ok := errors.AsType[*ExtensionLoadError](errs[0])
	if !ok {
		t.Fatalf("error %T %v is not an ExtensionLoadError", errs[0], errs[0])
	}
	want := "TypeScript extensions need Node.js 22.13 or newer; node was not found on PATH"
	if loadErr.Name != "greeter" || !strings.Contains(loadErr.Error(), want) || filepath.Base(loadErr.Path) != "greeter" {
		t.Fatalf("load error = name %q path %q: %v; want greeter and %q", loadErr.Name, loadErr.Path, loadErr, want)
	}
}

// A /reload keeps the embedded cells' manifest order: a native cell listed
// before a Node cell still ranks before it, as at startup, so the reloaded
// extensions, and every list ordered by them, do not change order.
func TestHost_ReloadKeepsTheEmbeddedCellOrderAroundANodeCell(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required for the embedded Node cell fixture: %v", err)
	}
	native := EmbeddedCell{
		Language: "go", Strategy: string(CellStrategyIsolated), Key: "fixture", BinaryPath: buildFixtureExt(t),
		Extensions: []EmbeddedExtension{{Name: "fixture", Hash: "h1"}},
	}
	host := NewHostWithConfigRoot(t.TempDir(), t.TempDir())
	host.SetConfigLoader(func() ([]ExtConfig, error) { return nil, nil })
	t.Cleanup(func() { host.Shutdown("test done") })
	names := func(extensions []extension.Extension) []string {
		out := make([]string, len(extensions))
		for i, ext := range extensions {
			out[i] = ext.Name
		}
		return out
	}
	loaded, errs := host.LoadEmbeddedCells(testbudget.Context(t), []EmbeddedCell{native, writeEmbeddedNodeCell(t)})
	if len(errs) != 0 {
		t.Fatalf("LoadEmbeddedCells errors: %v", errs)
	}
	want := []string{"fixture", "greeter"}
	if got := names(loaded); !slices.Equal(got, want) {
		t.Fatalf("startup order = %v, want %v", got, want)
	}
	reloaded, err := host.Reload(testbudget.Context(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := names(reloaded); !slices.Equal(got, want) {
		t.Fatalf("reload order = %v, want the startup order %v", got, want)
	}
	if got := names(host.Extensions()); !slices.Equal(got, want) {
		t.Fatalf("host order after reload = %v, want the startup order %v", got, want)
	}
}
