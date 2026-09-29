package tui

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
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
		d := NewLoginDialog("Test", nil)
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
