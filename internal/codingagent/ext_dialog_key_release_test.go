package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// TestExtensionDialogDropsKittyKeyRelease pins that an open extension dialog
// receives key presses only.
//
// pig pushes \x1b[>7u at startup, whose flag 2 makes the terminal report
// press/repeat/release event types, so every arrow keypress arrives as two
// chunks. dispatchKey filters releases before the editor and keybinding
// dispatch, but the extension-dialog branch returns earlier, so a dialog
// opened by ctx.Select/Confirm/Input saw both chunks and moved its cursor
// twice per keystroke.
//
// Mirrors upstream tui.ts:887, which drops releases immediately before
// focusedComponent.handleInput unless the component sets wantsKeyRelease.
func TestExtensionDialogDropsKittyKeyRelease(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		// Kitty Down: press (event type :1) then release (:3).
		{"kitty down press+release", []string{"\x1b[1;1:1B", "\x1b[1;1:3B"}, "beta"},
		// Press with no explicit event type; release still tagged :3.
		{"kitty down bare press + release", []string{"\x1b[1;1B", "\x1b[1;1:3B"}, "beta"},
		// Legacy terminals send no release at all; must still move exactly once.
		{"legacy down", []string{"\x1b[B"}, "beta"},
		// Two real presses must still move twice, proving the filter drops
		// releases rather than deduplicating repeated keys.
		{"two presses move twice", []string{"\x1b[1;1:1B", "\x1b[1;1:3B", "\x1b[1;1:1B", "\x1b[1;1:3B"}, "gamma"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sel := tui.NewExtensionSelector("pick", []string{"alpha", "beta", "gamma", "delta", "epsilon"})
			m := &InteractiveMode{extensionDialog: &extensionDialog{
				component: sel,
				handle:    func(data string) { sel.HandleInput(data) },
			}}

			for _, chunk := range tc.input {
				if err := m.dispatchKey(context.Background(), chunk); err != nil {
					t.Fatalf("dispatchKey(%q): %v", chunk, err)
				}
			}

			if got := sel.SelectedValue(); got != tc.want {
				t.Errorf("SelectedValue = %q; want %q (a dispatched key release moves the cursor twice)", got, tc.want)
			}
		})
	}
}
