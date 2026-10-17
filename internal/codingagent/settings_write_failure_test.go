package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// brokenGlobalSettings is a settings manager whose global settings.json does not parse, so every global write fails. Pi's setters queue
// the write and record the failure for the settings diagnostics (settings-manager.ts enqueueWrite, recordError); the caller sees none.
func brokenGlobalSettings(t *testing.T) *SettingsManager {
	t.Helper()
	agentDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewSettingsManager(t.TempDir(), agentDir)
}

// interactive-mode.ts:2670-2680: an extension's setTheme(name) returns the theme controller's result; a settings write that fails does not
// turn a theme that loaded into a failure.
func TestExtensionSetThemeSucceedsWhenTheSettingsWriteFails(t *testing.T) {
	f := themeControllerNew(t, "dark", nil)
	f.flush(t)
	f.m.opts.SettingsManager = brokenGlobalSettings(t)
	ui := &ExtUIContext{m: f.m}
	if result := ui.SetTheme(extension.ThemeName("light")); !result.Success {
		t.Fatalf("SetTheme(light) = %+v, want success: the theme loaded and Pi's settings write failure is only recorded", result)
	}
	if active := f.m.themeState.activeThemeName.Load(); active == nil || *active != "light" {
		t.Fatalf("active theme = %v, want light", active)
	}
}

// interactive-mode.ts:5438-5446 onPersist: setEnabledModels, then "Model selection saved to settings"; a write failure is recorded for the
// settings diagnostics and the status line is the same.
func TestScopedModelSelectionReportsSavedWhenTheSettingsWriteFails(t *testing.T) {
	m := &InteractiveMode{opts: InteractiveModeOptions{SettingsManager: brokenGlobalSettings(t)}, chatContainer: tui.NewContainer()}
	m.persistScopedModelIDs([]string{"provider/model"})
	if m.lastStatusMessage != "Model selection saved to settings" {
		t.Fatalf("status = %q, want Pi's saved status", m.lastStatusMessage)
	}
}
