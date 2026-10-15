package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Upstream core/keybindings.ts: reload() replaces the user bindings with loadFromFile(configPath), and loadRawConfig
// answers undefined (so no user bindings) for a missing file, a parse error or a non-object value, and strips a BOM.
// reload never throws.
// Pi: packages/coding-agent/src/core/keybindings.ts:385 (KeybindingsManager.reload); packages/tui/src/keybindings.ts:278 (KeybindingsManager.getKeys).
func TestKeybindingsManagerReloadMatchesUpstreamLoadFromFile(t *testing.T) {
	restoreTUIKeybindings(t)
	defaults := NewKeybindingsManagerFromBindings(nil, "").GetKeys("app.tools.expand")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, content string
		want          []KeyID
	}{
		{"valid", `{"app.tools.expand":"ctrl+g"}`, []KeyID{"ctrl+g"}},
		{"BOM is stripped", "\uFEFF" + `{"app.tools.expand":"ctrl+g"}`, []KeyID{"ctrl+g"}},
		{"parse error resets to defaults", `{"app.tools.expand":`, defaults},
		{"non-object resets to defaults", `3`, defaults},
		{"null resets to defaults", `null`, defaults},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "keybindings.json")
		km := NewKeybindingsManager(dir)
		km.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+y"}})
		write(path, tc.content)
		km.Reload()
		if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, tc.want) {
			t.Errorf("%s: bindings = %v, want %v", tc.name, got, tc.want)
		}
	}
	// A deleted file also resets the bindings.
	dir := t.TempDir()
	km := NewKeybindingsManager(dir)
	km.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+y"}})
	km.Reload()
	if !slices.Equal(km.GetKeys("app.tools.expand"), defaults) {
		t.Errorf("missing file: bindings = %v, want %v", km.GetKeys("app.tools.expand"), defaults)
	}
}

// Upstream `new KeybindingsManager()` has no configPath, and reload() returns at once: the user bindings stay and no file
// is read, not even a keybindings.json in the working directory.
func TestKeybindingsManagerWithoutConfigPathReloadKeepsUserBindings(t *testing.T) {
	restoreTUIKeybindings(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{"app.tools.expand":"ctrl+g"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	km := NewKeybindingsManagerFromBindings(nil, "")
	km.SetUserBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+y"}})
	km.Reload()
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+y"}) {
		t.Fatalf("bindings after Reload = %v, want the user bindings [ctrl+y]", got)
	}
}

// Upstream `new KeybindingsManager(userBindings, configPath)` (core/keybindings.ts:374), as interactive-tui.test.ts:85 builds it:
// the user bindings override the defaults, without a configPath reload keeps them, and with a configPath reload replaces them with the file.
func TestKeybindingsManagerFromTakesUserBindingsAndConfigPath(t *testing.T) {
	restoreTUIKeybindings(t)
	defaults := NewKeybindingsManager("").GetKeys("app.tools.expand")
	km := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+y"}}, "")
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+y"}) {
		t.Errorf("user binding = %v, want [ctrl+y]", got)
	}
	km.Reload()
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+y"}) {
		t.Errorf("a manager without a configPath lost its user bindings on reload: %v", got)
	}

	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"app.tools.expand":"ctrl+g"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	km = NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+y"}}, path)
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+y"}) {
		t.Errorf("construction read the config file: %v", got)
	}
	km.Reload()
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+g"}) {
		t.Errorf("reload with a configPath = %v, want the file's [ctrl+g]", got)
	}
	if got := NewKeybindingsManagerFromBindings(nil, "").GetKeys("app.tools.expand"); !slices.Equal(got, defaults) {
		t.Errorf("no user bindings = %v, want the defaults %v", got, defaults)
	}
}

// Pi: packages/coding-agent/src/core/keybindings.ts:374 (constructor(userBindings, configPath)) and :379 (create).
// `new KeybindingsManager(userBindings, configPath)` resolves the given user bindings at once, and reload() later replaces them with the file at configPath.
func TestNewKeybindingsManagerTakesUserBindingsAndConfigPath(t *testing.T) {
	restoreTUIKeybindings(t)
	defaults := NewKeybindingsManagerFromBindings(nil, "").GetKeys("app.tools.expand")
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	if err := os.WriteFile(path, []byte(`{"app.tools.expand":"ctrl+g"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	given := map[string][]KeyID{"app.tools.expand": {"ctrl+y"}}
	km := NewKeybindingsManagerFromBindings(given, path)
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+y"}) {
		t.Fatalf("constructed bindings = %v, want the given ctrl+y (defaults %v)", got, defaults)
	}
	given["app.tools.expand"][0] = "ctrl+z"
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+y"}) {
		t.Fatalf("the manager aliases the caller's slice: bindings = %v", got)
	}
	km.Reload()
	if got := km.GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+g"}) {
		t.Fatalf("after Reload bindings = %v, want the file's ctrl+g", got)
	}
	if got := NewKeybindingsManager(dir).GetKeys("app.tools.expand"); !slices.Equal(got, []KeyID{"ctrl+g"}) {
		t.Fatalf("create(agentDir) bindings = %v, want the file's ctrl+g", got)
	}
}

// Pi: packages/coding-agent/src/core/keybindings.ts:374,390 (KeybindingsConfig, getEffectiveConfig): a config binds an action to one key (a string) or an ordered list; the effective config reports a single binding as the string and several as the list.
func TestKeybindingsConfigTakesAStringOrAListAndTheEffectiveConfigReportsThem(t *testing.T) {
	restoreTUIKeybindings(t)
	km := NewKeybindingsManagerFromBindings(map[string][]KeyID{"app.tools.expand": {"ctrl+y"}, "app.session.rename": {"ctrl+r", "ctrl+n"}}, "")
	effective := km.GetEffectiveConfig()
	if got := effective["app.tools.expand"]; got.Key != "ctrl+y" || got.Keys != nil {
		t.Errorf("single binding = %#v, want the string ctrl+y", got)
	}
	if got := effective["app.session.rename"].Keys; !slices.Equal(got, []string{"ctrl+r", "ctrl+n"}) {
		t.Errorf("list binding = %#v, want [ctrl+r ctrl+n]", effective["app.session.rename"])
	}
}
