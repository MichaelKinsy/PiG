package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Pi settings-manager.ts:getExternalEditorCommand tests configured.trim() only for emptiness, returns the original command, and does not trim environment values.
func TestSettingsExternalEditorWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name, configured, visual, editor, want string
	}{
		{"blank configured", " \t\n", "vim", "nano", "vim"},
		{"JS blank configured", " \ufeff\t", "vim", "nano", "vim"},
		{"NEXT LINE is not JS whitespace", "\u0085", "vim", "nano", "\u0085"},
		{"configured is not trimmed", " code --wait ", "vim", "nano", " code --wait "},
		{"environment is not trimmed", "", " \t", "nano", " \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VISUAL", tc.visual)
			t.Setenv("EDITOR", tc.editor)
			raw, err := json.Marshal(Settings{ExternalEditor: tc.configured})
			settingsOK(t, err)
			sm := writeSettingsLayers(t, string(raw), `{}`)
			settingsEqual(t, sm.GetExternalEditorCommand(), tc.want)
		})
	}
}

func BenchmarkSettingsEditorAndPadding(b *testing.B) {
	for _, tc := range []struct{ name, configured, visual string }{
		{"configured", "code --wait", "vim"},
		{"environment", "", "vim"},
		{"platform", "", ""},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.Setenv("VISUAL", tc.visual)
			b.Setenv("EDITOR", "")
			sm := &SettingsManager{merged: Settings{
				ExternalEditor: tc.configured,
				DefaultTools:   []string{"read", "bash", "edit", "write"},
				Theme:          "dark", OutputPad: new(1),
			}}
			b.ReportAllocs()
			for b.Loop() {
				if sm.GetExternalEditorCommand() == "" || sm.GetOutputPad() != 1 {
					b.Fatal("invalid settings result")
				}
			}
		})
	}
}

func TestSettingsThemeAndEditorUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager.test.ts:203
	t.Run("stores slash-separated automatic theme settings separately from fixed theme names", func(t *testing.T) {
		sm := writeSettingsLayers(t, `{"theme":"light/dark"}`, `{}`)
		if got := sm.GetTheme(); got != "" {
			t.Fatalf("fixed theme=%q, want undefined", got)
		}
		setting := sm.GetThemeSetting()
		if setting == nil || *setting != "light/dark" {
			t.Fatalf("theme setting=%v", setting)
		}
		if err := sm.SetTheme("solarized-light/tokyo-night"); err != nil {
			t.Fatal(err)
		}
		settingsOK(t, sm.Flush())
		assertSettingsFileJSON(t, filepath.Join(sm.AgentDir(), "settings.json"), `{"theme":"solarized-light/tokyo-night"}`)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager.test.ts:439
	t.Run("should resolve editor commands by precedence", func(t *testing.T) {
		t.Setenv("VISUAL", "vim")
		t.Setenv("EDITOR", "nano")
		configured := NewInMemorySettingsManager(Settings{ExternalEditor: "code --wait"})
		if got := configured.GetExternalEditorCommand(); got != "code --wait" {
			t.Fatalf("configured editor=%q", got)
		}
		sm := NewInMemorySettingsManager(Settings{})
		if got := sm.GetExternalEditorCommand(); got != "vim" {
			t.Fatalf("VISUAL editor=%q", got)
		}
		if err := os.Unsetenv("VISUAL"); err != nil {
			t.Fatal(err)
		}
		t.Setenv("EDITOR", "emacs")
		if got := sm.GetExternalEditorCommand(); got != "emacs" {
			t.Fatalf("EDITOR command=%q", got)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/settings-manager.test.ts:450
	t.Run("should fall back to platform defaults", func(t *testing.T) {
		t.Setenv("VISUAL", "")
		t.Setenv("EDITOR", "")
		// Go's runtime.GOOS is immutable and calls Win32 "windows". Exercise every upstream platform through the production resolver, plus the public getter on the actual host.
		for _, tc := range []struct{ platform, goos, want string }{
			{"win32", "windows", "notepad"},
			{"darwin", "darwin", "nano"},
			{"linux", "linux", "nano"},
		} {
			t.Run(tc.platform, func(t *testing.T) {
				settingsEqual(t, resolveExternalEditorCommand("", "", "", tc.goos), tc.want)
				if runtime.GOOS == tc.goos {
					settingsEqual(t, NewInMemorySettingsManager(Settings{}).GetExternalEditorCommand(), tc.want)
				}
			})
		}
	})
}
