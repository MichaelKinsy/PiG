package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Upstream packages/coding-agent/test/theme-picker.test.ts:34 changes only dark.json's content name to bar and saves it as foo.json. Discovery must retain the content name and original source path.
func TestThemePickerUsesCustomThemeContentNames(t *testing.T) {
	data, err := builtinThemes.ReadFile("theme_dark.json")
	if err != nil {
		t.Fatal(err)
	}
	var theme map[string]json.RawMessage
	if err := json.Unmarshal(data, &theme); err != nil {
		t.Fatal(err)
	}
	theme["name"] = json.RawMessage(`"bar"`)
	data, err = json.MarshalIndent(theme, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "agent", "themes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "foo.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewThemeRegistry()
	if err := registry.LoadDir(dir); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(registry.Names(), "bar") || slices.Contains(registry.Names(), "foo") {
		t.Fatalf("available themes = %v, want bar but not foo", registry.Names())
	}
	if got := registry.PathOf("bar"); got != path {
		t.Fatalf("bar path = %q, want %q", got, path)
	}
	if registry.PathOf("foo") != "" {
		t.Fatal("filename registered as a theme name")
	}
}
