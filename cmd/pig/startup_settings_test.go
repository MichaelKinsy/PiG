package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// startupSettingsTheme returns the raw theme JSON the startup prompts read, or "" when the theme is unset.
func startupSettingsTheme(t *testing.T, settings codingagent.Settings) string {
	t.Helper()
	data, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return string(object["theme"])
}

// Pi 0.87.1 main.ts:656 creates the startup settings manager with project settings (projectTrusted defaults to true, settings-manager.ts:379), and main.ts:666-667 applies an interactive --use-theme on top. createStartupTui (startup-ui.ts:83-92) themes the Session picker (session-picker.ts:20), the missing-cwd prompt (main.ts:684), and the trust prompt (main.ts:749-756) from that manager.
func TestStartupPromptSettingsMatchPiStartupManager(t *testing.T) {
	t.Setenv("PI_HARDWARE_CURSOR", "")
	for _, tc := range []struct {
		name, project string
		args          []string
		interactive   bool
		theme         string
		cursor        bool
	}{
		{name: "project theme and terminal settings", project: `{"theme":"dark","showHardwareCursor":true}`, interactive: true, theme: `"dark"`, cursor: true},
		{name: "project null unsets the global theme", project: `{"theme":null}`, interactive: true},
		{name: "interactive --use-theme overrides the project", project: `{"theme":"dark"}`, args: []string{"--use-theme", "solarized"}, interactive: true, theme: `"solarized"`},
		{name: "interactive empty --use-theme is a selection", project: `{"theme":"dark"}`, args: []string{"--use-theme", ""}, interactive: true, theme: `""`},
		{name: "--use-theme outside interactive mode is ignored", project: `{"theme":"dark"}`, args: []string{"--use-theme", "solarized"}, theme: `"dark"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd, agentDir := t.TempDir(), t.TempDir()
			writeStartupSettings(t, filepath.Join(agentDir, "settings.json"), `{"theme":"light"}`)
			writeStartupSettings(t, filepath.Join(codingagent.ProjectConfigDir(cwd), "settings.json"), tc.project)
			manager := codingagent.NewSettingsManager(cwd, agentDir)
			opts := startupUIOptions(parseFlags(tc.args), tc.interactive, cwd, agentDir, manager)
			if got := startupSettingsTheme(t, opts.Settings); got != tc.theme {
				t.Errorf("startup theme = %s, want %s", got, tc.theme)
			}
			if got := opts.Settings.GetShowHardwareCursor(); got != tc.cursor {
				t.Errorf("hardware cursor = %v, want %v", got, tc.cursor)
			}
			if got := manager.GetGlobalSettings().Theme; got != "light" {
				t.Errorf("global theme = %q; the override must not reach the global layer", got)
			}
		})
	}
}

func writeStartupSettings(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
