package components

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/vterm"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// testKeys matches the tui.* ids through pi-tui's keymap and the app.* ids the components use through fixed keys.
type testKeys struct{}

var testAppKeys = map[string]string{
	"app.interrupt": "\x1b", "app.models.save": "\x13", "app.thinking.save": "\x13", "app.tree.editLabel": "\x14",
	"app.session.delete": "\x04",
}

func (testKeys) Matches(data, id string) bool {
	if strings.HasPrefix(id, "tui.") {
		return pitui.GetKeybindings().Matches(data, pitui.TUIKeybinding(id))
	}
	return testAppKeys[id] == data
}

// Keys makes the shared test context a SelectorContext.
func (*testContext) Keys() KeyMatcher { return testKeys{} }

const (
	keyEnter = "\r"
	keyEsc   = "\x1b"
	keyUp    = "\x1b[A"
	keyDown  = "\x1b[B"
)

// plain is the rows with escape sequences stripped and trailing spaces trimmed, joined by newlines.
func plain(lines []string) string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(widthx.StripTerminalSequences(line), " ")
	}
	return strings.Join(out, "\n")
}

// newScreen is an alt-screen TUI over a virtual terminal, stopped with the test.
func newScreen(t testing.TB, columns, rows int) (*pitui.TuiAltScreen, *vterm.Terminal) {
	t.Helper()
	terminal := vterm.New(columns, rows)
	screen := pitui.NewTuiAltScreen(terminal, nil, t.TempDir())
	t.Cleanup(screen.Stop)
	return screen, terminal
}

func assertWithin(t *testing.T, lines []string, width int) {
	t.Helper()
	for _, line := range lines {
		if w := widthx.VisibleWidth(line); w > width {
			t.Fatalf("line wider than %d (%d): %q", width, w, line)
		}
	}
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q in:\n%s", want, got)
	}
}

func lacks(t *testing.T, got, unwanted string) {
	t.Helper()
	if strings.Contains(got, unwanted) {
		t.Fatalf("unexpected %q in:\n%s", unwanted, got)
	}
}

func lightTheme(t *testing.T) (*theme.Theme, error) {
	t.Helper()
	light, _ := theme.BuiltinTheme("light")
	return theme.CreateTheme(light, pitui.TerminalColorModeTrueColor, theme.TerminalState{})
}
