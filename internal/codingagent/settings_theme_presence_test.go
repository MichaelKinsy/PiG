package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsThemePresenceRoundTrip(t *testing.T) {
	for _, wire := range []string{`{}`, `{"theme":""}`, `{"theme":"light"}`} {
		t.Run(wire, func(t *testing.T) {
			var settings Settings
			if err := json.Unmarshal([]byte(wire), &settings); err != nil {
				t.Fatal(err)
			}
			for _, copy := range []Settings{settings, cloneSettings(settings), mergeSettings(settings, Settings{})} {
				data, err := json.Marshal(copy)
				if err != nil || string(data) != wire {
					t.Fatalf("settings=%s want=%s err=%v", data, wire, err)
				}
			}
		})
	}
}

func TestSettingsEmptyThemeOverridesAndPersists(t *testing.T) {
	for _, previous := range []string{`{}`, `{"theme":"light"}`} {
		t.Run(previous, func(t *testing.T) {
			cwd, dir := t.TempDir(), t.TempDir()
			path := filepath.Join(dir, "settings.json")
			if err := os.WriteFile(path, []byte(previous), 0o600); err != nil {
				t.Fatal(err)
			}
			manager := NewSettingsManager(cwd, dir)
			if previous == `{}` && manager.GetThemeSetting() != nil {
				t.Fatal("omitted theme acquired a value")
			}
			if err := manager.SetTheme(""); err != nil {
				t.Fatal(err)
			}
			if err := manager.SetDefaultModel("model"); err != nil {
				t.Fatal(err)
			}
			manager.Reload()
			setting := manager.GetThemeSetting()
			if setting == nil || *setting != "" {
				t.Fatalf("empty theme getter=%v", setting)
			}
			*setting = "dark"
			if *manager.GetThemeSetting() != "" {
				t.Fatal("caller mutated retained settings through the getter")
			}
			assertThemeField(t, manager.Get(), new(""))
			assertThemeField(t, manager.GetGlobalSettings(), new(""))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(data, &object); err != nil || string(object["theme"]) != `""` {
				t.Fatalf("stored settings=%s err=%v", data, err)
			}
		})
	}
}

func TestSettingsProjectAndTransientEmptyThemeOverride(t *testing.T) {
	cwd, dir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"theme":"light"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewSettingsManager(cwd, dir)
	var empty Settings
	if err := json.Unmarshal([]byte(`{"theme":""}`), &empty); err != nil {
		t.Fatal(err)
	}
	manager.ApplyOverrides(empty)
	manager.ApplyOverrides(Settings{ShellPath: "shell"})
	assertThemeField(t, manager.Get(), new(""))
	assertThemeField(t, manager.GetGlobalSettings(), new("light"))
	manager.Reload()
	assertThemeField(t, manager.Get(), new("light"))
	projectDir := ProjectConfigDir(cwd)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "settings.json"), []byte(`{"theme":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager.Reload()
	assertThemeField(t, manager.Get(), new(""))
	assertThemeField(t, manager.GetProjectSettings(), new(""))
	if err := manager.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	assertThemeField(t, manager.Get(), new(""))
	assertThemeField(t, manager.GetGlobalSettings(), new("dark"))
}

func assertThemeField(t *testing.T, settings Settings, want *string) {
	t.Helper()
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	value, present := object["theme"]
	if want == nil {
		if present {
			t.Fatalf("unexpected theme field: %s", value)
		}
		return
	}
	expected, err := json.Marshal(*want)
	if err != nil || !present || string(value) != string(expected) {
		t.Fatalf("theme=%s present=%v want=%s err=%v", value, present, expected, err)
	}
}
