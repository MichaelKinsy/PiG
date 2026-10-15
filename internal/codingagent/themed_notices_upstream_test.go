package codingagent

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream 0.99.1 builds the chat notices with ThemedText (interactive-mode.ts:4492-4502 showError and showWarning, :3705-3736 status lines, :2942-2955 extension errors, :6231-6236 the reload notice) so a theme change, or the system theme receiving the terminal's colors, rebuilds them: the UI invalidates every component on a theme change and ThemedText rebuilds its string after an invalidation (themed-text.test.ts). The upstream tests cover the component; these cases cover the notices that use it.
func TestChatNoticesFollowThemeChangesUpstream(t *testing.T) {
	restoreStartupTheme(t)
	// In 256 colors the dark and light dim colors can quantize to one index; a terminal that reports truecolor is the case the assertions compare.
	previousCaps := tui.GetCapabilities()
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	for _, tc := range []struct {
		name  string
		token string
		add   func(m *InteractiveMode)
	}{
		{"an error", "error", func(m *InteractiveMode) { m.showError("boom") }},
		{"a warning", "warning", func(m *InteractiveMode) { m.showWarning("careful") }},
		{"a status line", "dim", func(m *InteractiveMode) { m.showStatus("working") }},
		{"an extension error", "error", func(m *InteractiveMode) { m.showExtensionError("ext.ts", "failed", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tui.SetThemeByName("dark")
			m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 100, 30)}
			tc.add(m)
			dark := tui.ActiveTheme().GetFgAnsi(tc.token)
			if got := strings.Join(m.chatContainer.Render(100), "\n"); !strings.Contains(got, dark) {
				t.Fatalf("notice %q lacks the dark %s color %q", got, tc.token, dark)
			}

			tui.SetThemeByName("light")
			light := tui.ActiveTheme().GetFgAnsi(tc.token)
			if light == dark {
				t.Fatalf("the built-in themes share the %s color %q", tc.token, light)
			}
			// The UI invalidates every component when the theme changes.
			m.chatContainer.Invalidate()
			got := strings.Join(m.chatContainer.Render(100), "\n")
			if !strings.Contains(got, light) || strings.Contains(got, dark) {
				t.Errorf("notice after the theme change = %q, want the light color %q and not %q", got, light, dark)
			}
		})
	}
}
