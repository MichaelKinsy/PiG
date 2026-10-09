package tui

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The oracle exercises Pi 0.87.1 showAuthPrompt with type:secret, then renders
// the complete dialog before editing, during editing, after submission and after progress.
func TestLoginDialogMaskDisabledMatchesPi(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("FORCE_COLOR", "1")
	// The theme reads the cached capabilities; drop what an earlier test detected and what this one detects.
	ResetCapabilitiesCache()
	t.Cleanup(ResetCapabilitiesCache)
	previous := ActiveTheme()
	SetTheme("dark")
	defer storeActiveTheme(previous)
	data, err := exec.CommandContext(t.Context(), "node", filepath.Join(root, "test/parity/testdata/login-dialog-privacy.mjs"), root).CombinedOutput()
	if err != nil {
		t.Fatalf("Pi oracle: %v: %s", err, data)
	}
	var want [][]string
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	var got [][]string
	for _, value := range []string{"", "abcd", "abcde-12345", "x😀界éZ"} {
		d := NewLoginDialogComponent(nil, "Test", nil, "")
		d.SetFocused(true) // the oracle script sets dialog.focused = true before the prompt
		d.SetMaskSecretInput(false)
		answer := d.ShowSecretInput("API key", "sample")
		got = append(got, d.Render(100))
		d.HandleInput(value)
		got = append(got, d.Render(100))
		d.HandleInput("\r")
		if submitted := <-answer; submitted != value {
			t.Fatal("submission changed")
		}
		got = append(got, d.Render(100))
		d.ShowProgress("Checking credentials...")
		got = append(got, d.Render(100))
	}
	if len(got) != len(want) {
		t.Fatalf("frame count %d != oracle %d", len(got), len(want))
	}
	for i := range got {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("frame %d differs\nPiG: %q\nPi:  %q", i, got[i], want[i])
		}
	}
}

// login-dialog.ts:21-29: the dialog is Focusable and its focused setter assigns input.focused, so the prompt input emits the
// hardware-cursor marker only while the dialog holds TUI focus.
func TestLoginDialogFocusPropagatesToPromptInput(t *testing.T) {
	render := func(d *LoginDialogComponent) string { return strings.Join(d.Render(60), "\n") }
	d := NewLoginDialogComponent(nil, "Acme", nil, "")
	var _ Focusable = d
	if d.Focused() {
		t.Fatal("a new dialog is focused")
	}
	d.ShowPrompt("Paste the code", "")
	if strings.Contains(render(d), widthx.CursorMarker) {
		t.Fatal("an unfocused dialog rendered the hardware cursor marker")
	}
	d.SetFocused(true)
	if !d.Focused() || !strings.Contains(render(d), widthx.CursorMarker) {
		t.Fatalf("focusing the dialog did not focus its prompt input: focused=%v", d.Focused())
	}
	d.SetFocused(false)
	if strings.Contains(render(d), widthx.CursorMarker) {
		t.Fatal("unfocusing the dialog left the prompt input focused")
	}
	// A prompt shown while the dialog is focused starts focused.
	d.SetFocused(true)
	d.HandleInput("\r")
	d.ShowPrompt("Second prompt", "")
	if !strings.Contains(render(d), widthx.CursorMarker) {
		t.Fatal("a prompt shown in a focused dialog was not focused")
	}
}
