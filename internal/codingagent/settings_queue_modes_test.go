package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// settings-manager.ts setSteeringMode and setFollowUpMode write the global settings file and nothing else: the other queue mode, the other keys
// and the project settings stay as they were, and a new manager reads the mode back.
func TestSettingsManagerQueueModeSettersWriteTheirOwnGlobalKey(t *testing.T) {
	cwd := t.TempDir()
	agentDir := t.TempDir()
	path := filepath.Join(agentDir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"theme":"dark","steeringMode":"one-at-a-time","followUpMode":"one-at-a-time"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSettingsManager(cwd, agentDir)
	persisted := func() map[string]any {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}

	if err := sm.SetSteeringMode("all"); err != nil {
		t.Fatal(err)
	}
	if got := persisted(); got["steeringMode"] != "all" || got["followUpMode"] != "one-at-a-time" || got["theme"] != "dark" {
		t.Fatalf("after SetSteeringMode(all) the file holds %#v, want only steeringMode changed", got)
	}
	if sm.GetSteeringMode() != "all" || sm.GetFollowUpMode() != "one-at-a-time" {
		t.Fatalf("modes after SetSteeringMode(all) = %q/%q, want all/one-at-a-time", sm.GetSteeringMode(), sm.GetFollowUpMode())
	}

	if err := sm.SetFollowUpMode("all"); err != nil {
		t.Fatal(err)
	}
	if got := persisted(); got["steeringMode"] != "all" || got["followUpMode"] != "all" || got["theme"] != "dark" {
		t.Fatalf("after SetFollowUpMode(all) the file holds %#v, want only followUpMode changed", got)
	}

	if err := sm.SetSteeringMode("one-at-a-time"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewSettingsManager(cwd, agentDir)
	if reloaded.GetSteeringMode() != "one-at-a-time" || reloaded.GetFollowUpMode() != "all" {
		t.Fatalf("reloaded modes = %q/%q, want one-at-a-time/all", reloaded.GetSteeringMode(), reloaded.GetFollowUpMode())
	}
	if _, err := os.Stat(filepath.Join(cwd, ConfigDirName(), "settings.json")); err == nil {
		t.Fatal("the queue mode setters wrote the project settings file")
	}
}
