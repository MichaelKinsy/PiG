package codingagent

// pi: packages/coding-agent/src/core/settings-manager.ts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pilock"
)

// holdSettingsLock takes the lock directory of the settings.json in dir, as another process that holds proper-lockfile's lock does: every save of the file fails with ELOCKED and the file stays readable. The lock's time is ahead of the clock, so no attempt finds it stale.
func holdSettingsLock(t *testing.T, dir string) {
	t.Helper()
	lock := filepath.Join(dir, "settings.json.lock")
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	ahead := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(lock, ahead, ahead); err != nil {
		t.Fatal(err)
	}
}

// D633. The model selector of an interactive run without a Session saves the default model itself (Ctrl+S). Pi's setter cannot throw, so Pi's selector shows "Default model: ..." whatever became of the write (interactive-mode.ts:5313-5325); PiG's shows the save error instead, and the settings file keeps the previous default.
func TestModelSelectorShowsAFailedSaveOfTheDefaultModel(t *testing.T) {
	m := modelPickerTestMode(t)
	t.Setenv("OPENAI_API_KEY", "fake-openai")
	target := ai.ListModels("openai")[0].ID
	m.opts.ModelBuilder = func(string) (*ai.Model, error) {
		return &ai.Model{ID: target, Provider: captureStreamOptionsProvider{}}, nil
	}
	holdSettingsLock(t, m.opts.AgentDir)
	input, release := m.acquireModalInputChannel()
	defer release()
	input <- []byte("\x13")
	input <- []byte("\x1b")
	sc := m.buildSlashContext(context.Background())
	var statuses, failures []string
	sc.ShowStatus = func(text string) { statuses = append(statuses, text) }
	sc.ShowError = func(text string) { failures = append(failures, text) }
	spec, ok := sc.PickModel(target)
	if !ok {
		t.Fatal("Ctrl+S did not accept the selection")
	}
	switchModelThen(sc, spec)
	runOwnerTasksUntil(t, m, func() bool { return len(statuses)+len(failures) > 0 })
	if len(statuses) != 0 || len(failures) != 1 || failures[0] != pilock.ErrLocked.Error() {
		t.Fatalf("statuses=%q errors=%q, want no status and the one error %q", statuses, failures, pilock.ErrLocked)
	}
	// The held lock refuses a reload too, so the file itself is read.
	var saved struct {
		DefaultModel string `json:"defaultModel"`
	}
	if data, err := os.ReadFile(filepath.Join(m.opts.AgentDir, "settings.json")); err != nil || json.Unmarshal(data, &saved) != nil || saved.DefaultModel != "original" {
		t.Fatalf("settings.json holds default model %q (read error %v), want the saved original", saved.DefaultModel, err)
	}
}

// D633. An extension's SettingsManager setter returns the failed save to the extension, where Pi's setters return nothing (settings-manager.ts:576-588); the change stays in memory, as in Pi.
func TestGraphSettingsManagerSettersReturnAFailedSave(t *testing.T) {
	agentDir := t.TempDir()
	graph := &GraphSettingsManager{manager: NewSettingsManager(t.TempDir(), agentDir)}
	holdSettingsLock(t, agentDir)
	for _, call := range []struct{ method, args string }{{"setTheme", `["dark"]`}, {"setSteeringMode", `["all"]`}} {
		if _, err := graph.Call(context.Background(), call.method, json.RawMessage(call.args)); !isProperLockfileContention(err) {
			t.Errorf("%s with the settings lock held = %v, want proper-lockfile's ELOCKED error", call.method, err)
		}
	}
	if got := graph.manager.GetSteeringMode(); got != "all" {
		t.Errorf("steering mode in memory = %q, want all", got)
	}
}
