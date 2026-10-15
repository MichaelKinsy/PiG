package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// upstream: packages/coding-agent/src/core/settings-manager.ts:292-294,465-467,496-499: create(cwd, agentDir, {projectTrusted}) defaults projectTrusted to true and an untrusted project contributes no settings.
func TestSettingsManagerCreateOptionsProjectTrust(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"theme":"global-theme"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ProjectConfigDir(cwd), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ProjectConfigDir(cwd), "settings.json"), []byte(`{"theme":"project-theme"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, untrusted := true, false
	for _, tc := range []struct {
		name string
		opts SettingsManagerCreateOptions
		want string
	}{
		{"omitted defaults to trusted", SettingsManagerCreateOptions{}, "project-theme"},
		{"explicit trusted", SettingsManagerCreateOptions{ProjectTrusted: &trusted}, "project-theme"},
		{"untrusted skips the project file", SettingsManagerCreateOptions{ProjectTrusted: &untrusted}, "global-theme"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := NewSettingsManagerWithOptions(cwd, agentDir, tc.opts).GetTheme(); got != tc.want {
				t.Fatalf("theme = %q, want %q", got, tc.want)
			}
		})
	}
}
