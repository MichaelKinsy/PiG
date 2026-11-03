package extension

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// keybindingHints is the surface upstream's KeybindingHints interface gives a tool render context, a message render
// options value and an entry render options value (packages/coding-agent/src/core/extensions/types.ts).
type keybindingHints interface {
	KeyText(keybinding string) string
	KeyHint(keybinding, description string) string
}

// Upstream keyText answers the keys bound in the host's registry joined by "/" ("" when none are) and keyHint is that
// text in the dim color followed by the description in the muted color (keybinding-hints.ts:34-42, 62-68). The three
// carriers must answer from the registry when called, so one value serves every later keybindings reload.
func TestKeybindingHintsAnswerFromTheHostRegistryOnEveryCarrier(t *testing.T) {
	previous := tui.GetTUIKeybindings()
	defer tui.SetTUIKeybindings(previous)

	carriers := map[string]keybindingHints{
		"ToolRenderContext":    ToolRenderContext{},
		"MessageRenderOptions": MessageRenderOptions{},
		"EntryRenderOptions":   EntryRenderOptions{},
	}
	for name, carrier := range carriers {
		t.Run(name, func(t *testing.T) {
			tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(map[string][]string{"tui.select.confirm": {"enter", "ctrl+j"}}))
			if got := carrier.KeyText("tui.select.confirm"); got != "enter/ctrl+j" {
				t.Fatalf("KeyText = %q, want the bound keys joined by /", got)
			}
			if got := carrier.KeyText("no.such.action"); got != "" {
				t.Fatalf("an unbound action has no key text, got %q", got)
			}
			theme := tui.ActiveTheme()
			want := theme.Fg("dim", "enter/ctrl+j") + theme.Fg("muted", " to confirm")
			if got := carrier.KeyHint("tui.select.confirm", "to confirm"); got != want {
				t.Fatalf("KeyHint = %q, want %q", got, want)
			}

			tui.SetTUIKeybindings(tui.NewTUIKeybindingsManager(map[string][]string{"tui.select.confirm": {"space"}}))
			if got := carrier.KeyText("tui.select.confirm"); got != "space" {
				t.Fatalf("KeyText after a keybindings reload = %q, want space", got)
			}
			if got := carrier.KeyHint("tui.select.confirm", "to confirm"); got != theme.Fg("dim", "space")+theme.Fg("muted", " to confirm") {
				t.Fatalf("KeyHint after a keybindings reload = %q", got)
			}
		})
	}
}
