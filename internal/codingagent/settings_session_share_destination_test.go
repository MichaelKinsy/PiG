package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Pi cea25743 settings-manager.ts getSessionShareDestination: "gist" and "radius" are returned as set, and every other value, including an absent one, reads as SETTINGS_DEFAULTS.sessionShareDestination ("auto"). The getter reads the merged settings, so a project value overrides the global one.
func TestSettingsManager_GetSessionShareDestination(t *testing.T) {
	for _, c := range []struct {
		value SessionShareDestination
		want  SessionShareDestination
	}{
		{"", SessionShareDestinationAuto},
		{"auto", SessionShareDestinationAuto},
		{"gist", SessionShareDestinationGist},
		{"radius", SessionShareDestinationRadius},
		{"GIST", SessionShareDestinationAuto},
		{"pi-in-go", SessionShareDestinationAuto},
	} {
		sm := &SettingsManager{merged: Settings{SessionShareDestination: c.value}}
		if got := sm.GetSessionShareDestination(); got != c.want {
			t.Errorf("GetSessionShareDestination(%q) = %q, want %q", c.value, got, c.want)
		}
	}

	sm := &SettingsManager{merged: Settings{SessionShareDestination: "radius"}, global: Settings{SessionShareDestination: "gist"}}
	if got := sm.GetSessionShareDestination(); got != SessionShareDestinationRadius {
		t.Fatalf("merged project override = %q, want radius", got)
	}
}

// Pi cea25743 settings-manager.ts setSessionShareDestination writes the global settings file, keeps other keys, and a mistyped value in the file reads as "auto".
func TestSettingsManager_SetSessionShareDestinationPersistsGlobally(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","sessionShareDestination":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	if got := sm.GetSessionShareDestination(); got != SessionShareDestinationAuto {
		t.Fatalf("mistyped value = %q, want auto", got)
	}
	if err := sm.SetSessionShareDestination(SessionShareDestinationGist); err != nil {
		t.Fatal(err)
	}
	if got := sm.GetSessionShareDestination(); got != SessionShareDestinationGist {
		t.Fatalf("after set = %q, want gist", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["sessionShareDestination"] != "gist" || persisted["theme"] != "dark" {
		t.Fatalf("persisted settings = %#v", persisted)
	}
	if got := NewSettingsManager(cwd, agentDir).GetSessionShareDestination(); got != SessionShareDestinationGist {
		t.Fatalf("reloaded = %q, want gist", got)
	}
}
