package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 extension-input.ts:94-104 intercepts tui.select.confirm. The
// embedded Input's separate tui.input.submit action has no submit callback.
func TestExtensionDialogRemappingThroughOwnerLoop(t *testing.T) {
	previous := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	m, output := newExtensionDialogProbe(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keybindings.json"), []byte(`{"tui.select.confirm":["ctrl+s"],"tui.select.cancel":["ctrl+q"],"tui.select.down":["ctrl+x"],"app.tools.expand":["ctrl+e"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.keybindings = NewKeybindingsManager(dir)
	ui := &ExtUIContext{m: m}
	got, err := runExtensionDialogProbe(t, m, func() (string, error) {
		return ui.Select(t.Context(), "Pick", []string{"", "selected"}, nil)
	}, []string{"\x05", "\x13", "\x18", "\x13"})
	if !m.toolsExpanded {
		t.Error("remapped expansion did not reach the transcript owner")
	}
	if err != nil || got != "selected" {
		t.Fatalf("Select = %q, %v; want selected", got, err)
	}
	got, err = runExtensionDialogProbe(t, m, func() (string, error) {
		return ui.Input(context.Background(), "Input", "", nil)
	}, []string{"first", "\r", "-second", "\x13"})
	if err != nil || got != "first-second" {
		t.Fatalf("Input = %q, %v; want first-second", got, err)
	}
	if plain := widthx.StripAnsi(output.String()); !strings.Contains(plain, "ctrl+s submit") || !strings.Contains(plain, "ctrl+q cancel") {
		t.Fatalf("owner loop did not render configured hints: %q", plain)
	}
}
