package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1 interactive-mode.ts:4073-4091 gates on trust and resources, not quietStartup or loaded extensions.
func TestProjectTrustWarningRendering(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		trusted, resources, history, quiet bool
	}{
		{name: "empty project", trusted: false},
		{name: "trusted resources", trusted: true, resources: true},
		{name: "denied fresh", resources: true},
		{name: "denied quiet", resources: true, quiet: true},
		{name: "denied resumed", resources: true, history: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cwd := t.TempDir()
			if tc.resources {
				if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			m := &InteractiveMode{
				opts:          InteractiveOptions{CWD: cwd, Settings: Settings{QuietStartup: tc.quiet}, SettingsManager: NewSettingsManagerWithProjectTrust(cwd, t.TempDir(), tc.trusted)},
				chatContainer: tui.NewContainer(),
			}
			if tc.history {
				m.chatContainer.Add(tui.NewText("persisted transcript"))
			}
			m.renderProjectTrustWarningIfNeeded()
			for _, width := range []int{20, 80, 180} {
				want := tui.NewContainer()
				if tc.history {
					want.Add(tui.NewText("persisted transcript"))
				}
				if !tc.trusted && tc.resources {
					if tc.history {
						want.Add(tui.NewSpacer(1))
					}
					want.Add(tui.NewPaddedText(tui.ActiveTheme().FgText("warning", "This project is not trusted. Project .pig resources and packages are ignored. Use /trust to save a trust decision, then restart pig."), 1, 0, nil))
				}
				if got, expected := m.chatContainer.Render(width), want.Render(width); !slices.Equal(got, expected) {
					t.Errorf("width %d: rows = %q, want %q", width, got, expected)
				}
			}
		})
	}
}

func BenchmarkProjectTrustWarning(b *testing.B) {
	cwd := b.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, CONFIG_DIR_NAME, "extensions"), 0o755); err != nil {
		b.Fatal(err)
	}
	m := &InteractiveMode{opts: InteractiveOptions{CWD: cwd, SettingsManager: NewSettingsManagerWithProjectTrust(cwd, b.TempDir(), false)}, chatContainer: tui.NewContainer()}
	b.ReportAllocs()
	for b.Loop() {
		m.chatContainer.Clear()
		m.renderProjectTrustWarningIfNeeded()
		m.chatContainer.Render(80)
	}
}
