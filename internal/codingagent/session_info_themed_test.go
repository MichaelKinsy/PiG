package codingagent

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// interactive-mode.ts handleSessionCommand adds `new ThemedText(renderInfo, 1, 0)`: the statistics are a snapshot taken by the command, and
// renderInfo builds the text again on every render, so the /session block follows a later theme change as a fresh /session in that theme shows it.
func TestSessionCommandBlockFollowsThemeChanges(t *testing.T) {
	previous, previousCaps := tui.ActiveTheme(), tui.GetCapabilities()
	t.Cleanup(func() { tui.SetTheme(previous.Name) })
	t.Cleanup(func() { tui.SetCapabilities(previousCaps) })
	// The block styles only with bold and the dim token, and the two themes' dim colors (okhsl(229 8% 56%) and okhsl(229 7% 59%)) are
	// both index 102 in 256-color mode, in Pi 1.2.0's foregroundAnsi as in Pig's, so only truecolor tells the themes apart.
	tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: true})
	m := modelPickerTestMode(t)
	run := func() []string {
		m.chatContainer.Clear()
		if err := sessionHandler(m.buildSlashContext(t.Context())); err != nil {
			t.Fatal(err)
		}
		return m.chatContainer.Render(80)
	}
	tui.SetTheme("dark")
	dark := run()
	m.chatContainer.Clear()
	if err := sessionHandler(m.buildSlashContext(t.Context())); err != nil {
		t.Fatal(err)
	}
	tui.SetTheme("light")
	switched := m.chatContainer.Render(80)
	light := run()
	if slices.Equal(dark, light) {
		t.Fatal("the dark and light /session blocks are the same; the test cannot tell the themes apart")
	}
	if !slices.Equal(switched, light) {
		t.Fatalf("after the theme change the /session block is\n%q\nwant the light one\n%q", switched, light)
	}
}
