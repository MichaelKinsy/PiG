package codingagent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// The extension settings encoding is made once per settings generation: unchanged settings hand back the same bytes without encoding, and
// every kind of settings write makes a new generation whose bytes hold ExtensionSettings() (EX-F4).
func TestExtensionSettingsJSONEncodesOncePerSettingsGeneration(t *testing.T) {
	sm := NewInMemorySettingsManager(settingsWithEveryKind(t))
	check := func(step string) json.RawMessage {
		t.Helper()
		got := sm.ExtensionSettingsJSON()
		var decoded map[string]any
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatal(err)
		}
		if want := map[string]any(sm.ExtensionSettings()); !reflect.DeepEqual(decoded, want) {
			t.Fatalf("%s: ExtensionSettingsJSON\n got %s\nwant %v", step, got, want)
		}
		if again := sm.ExtensionSettingsJSON(); unsafe.SliceData(again) != unsafe.SliceData(got) { //nolint:gosec // G103: compares backing-array addresses to prove the encoding is reused; the pointers are never dereferenced
			t.Fatalf("%s: unchanged settings were encoded again", step)
		}
		if allocs := testing.AllocsPerRun(50, func() { _ = sm.ExtensionSettingsJSON() }); allocs != 0 {
			t.Fatalf("%s: ExtensionSettingsJSON of unchanged settings: %v allocs, want 0", step, allocs)
		}
		return got
	}
	previous := check("initial")
	writes := []struct {
		name  string
		write func()
	}{
		{"UpdateGlobal", func() { _ = sm.SetDefaultModel("next-model") }},
		{"UpdateProject", func() { _ = sm.UpdateProject(func(s *Settings) { s.Transport = "websocket" }) }},
		{"ApplyOverrides", func() { sm.ApplyOverrides(ThemeOverride("dark")) }},
		{"SetProjectTrusted", func() { sm.SetProjectTrusted(false) }},
		{"Reload", func() { sm.Reload() }},
	}
	for _, w := range writes {
		w.write()
		got := check(w.name)
		if unsafe.SliceData(got) == unsafe.SliceData(previous) { //nolint:gosec // G103: compares backing-array addresses to prove the encoding was regenerated; the pointers are never dereferenced
			t.Fatalf("%s: the write kept the previous generation's encoding", w.name)
		}
		previous = got
	}
	if !bytes.Contains(sm.ExtensionSettingsJSON(), []byte(`"next-model"`)) {
		t.Fatalf("the encoding misses the global write: %s", sm.ExtensionSettingsJSON())
	}
	settings := sm.ExtensionSettings()
	settings["mutated-by-test"] = true
	if _, ok := sm.ExtensionSettings()["mutated-by-test"]; ok {
		t.Fatal("ExtensionSettings returned shared state")
	}
}

// Pi's getSettings() clones this.settings, deepMergeSettings(globalSettings, projectSettings) of the migrated file objects
// (settings-manager.ts:195-212, 566-568), so an extension sees the members in the files' order: global's own, then the members
// only the project file sets, and a nested object merges the same way. A setter assigns a new member after the others, and
// applyOverrides merges into the merged object. Go sorted them.
func TestExtensionSettingsJSONKeepsPiMemberOrder(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(agentDir, "settings.json"), `{"theme":"dark","queueMode":"all","defaultProvider":"anthropic","compaction":{"reserveTokens":100,"enabled":false},"zzz":1,"aaa":{"y":1,"b":2}}`)
	write(filepath.Join(ProjectConfigDir(cwd), "settings.json"), `{"aaa":{"c":3},"compaction":{"keepRecentTokens":5},"lastChangelogVersion":"1.0.0"}`)
	sm := NewSettingsManager(cwd, agentDir)
	keys := func(raw []byte, path ...string) []string {
		t.Helper()
		object, err := orderedjson.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		for _, key := range path {
			member, _ := object.Get(key)
			if object, err = orderedjson.Parse(member); err != nil {
				t.Fatalf("%s.%s: %v", raw, key, err)
			}
		}
		return object.Keys()
	}
	check := func(step string, want []string) {
		t.Helper()
		got := sm.ExtensionSettingsJSON()
		if top := keys(got); !slices.Equal(top, want) {
			t.Fatalf("%s: members %q, want %q (%s)", step, top, want, got)
		}
		if nested := keys(got, "compaction"); !slices.Equal(nested, []string{"reserveTokens", "enabled", "keepRecentTokens"}) {
			t.Fatalf("%s: compaction members %q (%s)", step, nested, got)
		}
		if nested := keys(got, "aaa"); !slices.Equal(nested, []string{"y", "b", "c"}) {
			t.Fatalf("%s: aaa members %q (%s)", step, nested, got)
		}
	}
	// migrateSettings moves queueMode to steeringMode, a member set after the file's own.
	loaded := []string{"theme", "defaultProvider", "compaction", "zzz", "aaa", "steeringMode", "lastChangelogVersion"}
	check("loaded", loaded)
	if err := sm.SetDefaultModel("next-model"); err != nil {
		t.Fatal(err)
	}
	written := []string{"theme", "defaultProvider", "compaction", "zzz", "aaa", "steeringMode", "defaultModel", "lastChangelogVersion"}
	check("after setDefaultModel", written)
	sm.ApplyOverrides(ThemeOverride("light"))
	check("after applyOverrides", written)
	sm.Reload()
	check("after reload", written)
}
