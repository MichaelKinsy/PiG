package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// writeLegacySDKAskModule writes a factory shaped like the owner's Pig 0.84
// ask extension: it imports the legacy SDK module path and replaces it with a
// sibling .sdk directory that holds an old, incompatible SDK.
func writeLegacySDKAskModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	extensionRoot := filepath.Join(root, "ask")
	for path, content := range map[string]string{
		filepath.Join(root, ".sdk", "go.mod"):  "module " + extsource.LegacyGoSDKModulePath + "\n\ngo 1.26\n",
		filepath.Join(root, ".sdk", "sdk.go"):  "package sdk\n\ntype Extension struct{}\n",
		filepath.Join(extensionRoot, "go.mod"): "module example.com/legacy/ask\n\ngo 1.26\n\nrequire " + extsource.LegacyGoSDKModulePath + " v0.0.0\n\nreplace " + extsource.LegacyGoSDKModulePath + " => ../.sdk\n",
		filepath.Join(extensionRoot, "main.go"): `package ask

import "github.com/mainstai/pig/extensions/sdk"

func Extension() *sdk.Extension {
	ext := sdk.New("ask")
	ext.Tool("ask_user", "ask the user", sdk.Schema{"type": "object"}, func(sdk.Context, map[string]any) (any, error) {
		return map[string]any{"content": "legacy ask ok"}, nil
	})
	ext.Command("ask", "start an interview", func(sdk.Context, string) error { return nil })
	return ext
}
`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return extensionRoot
}

// TestHost_LegacySDKFactoryLoadsBesideCurrentSDKFactory pins AK-001 through
// the full startup path: a Pig 0.84 factory resolves as a factory, plans into
// its own cell (never packed with current-SDK factories), builds against the
// current SDK, registers, and executes.
func TestHost_LegacySDKFactoryLoadsBesideCurrentSDKFactory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	legacy, legacyDefinition, err := ResolveExtConfigWithIdentity(writeLegacySDKAskModule(t), "ask")
	if err != nil {
		t.Fatalf("resolve legacy factory: %v", err)
	}
	if legacyDefinition.SDKModulePath != extsource.LegacyGoSDKModulePath || legacy.SDKName != extsource.LegacyGoSDKModulePath || legacy.Factory != "Extension" || legacy.EntrypointKind != "factory" {
		t.Fatalf("legacy config = %+v definition = %+v", legacy, *legacyDefinition)
	}
	current, _, err := ResolveExtConfigWithIdentity(writePackedFactoryModule(t, "example.com/current/ext", "current", "current_tool"), "current")
	if err != nil {
		t.Fatalf("resolve current factory: %v", err)
	}
	if current.SDKName != extsource.GoSDKModulePath {
		t.Fatalf("current SDKName = %q", current.SDKName)
	}
	cells := PlanCells([]ExtConfig{legacy, current}, nil)
	if len(cells) != 2 || cells[0].SDK == cells[1].SDK {
		t.Fatalf("legacy and current SDK factories share a cell: %+v", cells)
	}

	h := NewHost(t.TempDir())
	h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{legacy, current}, nil })
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if _, err := h.Reload(ctx); err != nil {
		t.Fatalf("reload: %v", err)
	}
	var found bool
	for _, ext := range h.Extensions() {
		tool, ok := ext.Tools["ask_user"]
		if !ok {
			continue
		}
		found = true
		if _, ok := ext.Commands["ask"]; !ok {
			t.Fatalf("legacy factory commands = %v", ext.Commands)
		}
		result, err := tool.Definition.Execute(ctx, "tc-legacy", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatalf("execute legacy tool: %v", err)
		}
		if got, ok := result.(agent.AgentToolResult); !ok || got.Content != "legacy ask ok" {
			t.Fatalf("legacy tool result = %#v", result)
		}
	}
	if !found || h.ExtensionCount() != 2 {
		t.Fatalf("legacy factory registered = %v, extension count = %d", found, h.ExtensionCount())
	}
}
