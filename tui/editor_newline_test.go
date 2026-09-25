package tui

import "testing"

// Ports the literal newline fallbacks from editor.ts handleInput. They remain
// editor behavior even when the corresponding bytes are not a keys.ts KeyId.
func TestEditorHardcodedNewlineEncodings(t *testing.T) {
	previousBindings := GetTUIKeybindings()
	previousKitty := IsKittyProtocolActive()
	t.Cleanup(func() {
		SetTUIKeybindings(previousBindings)
		SetKittyProtocolActive(previousKitty)
	})
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{KBInputNewLine: {}}))

	cases := []struct {
		name  string
		kitty bool
		input string
	}{
		{"CSI tilde legacy mode", false, "\x1b[13;2~"},
		{"CSI tilde Kitty mode", true, "\x1b[13;2~"},
		{"ESC CR legacy mode", false, "\x1b\r"},
		{"ESC CR Kitty mode", true, "\x1b\r"},
		{"LF-prefixed input", false, "\nignored"},
		{"input containing ESC and CR", false, "x\x1by\rz"},
		{"raw LF", false, "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetKittyProtocolActive(tc.kitty)
			editor := NewEditor()
			editor.HandleInput("a")
			editor.HandleInput(tc.input)
			editor.HandleInput("b")
			if got := editor.Text(); got != "a\nb" {
				t.Fatalf("HandleInput(%q) text = %q, want %q", tc.input, got, "a\nb")
			}
		})
	}
}
