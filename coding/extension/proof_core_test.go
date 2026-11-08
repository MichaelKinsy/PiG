package extension

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// KeybindingHints.keyText / keyHint (Pi types.ts:149-157) on the tool render context and on the message and entry render options format
// the host's configured keys: KeyText is the registry's keys, KeyHint is KeyText in the dim color then the description in the muted color.
func TestKeybindingHintsOfEveryRenderOptionFormatTheHostKeys(t *testing.T) {
	const action = "tui.select.confirm"
	want := tui.ActionKeyText(action)
	if want == "" {
		t.Fatalf("the host binds no key to %s, so the hints cannot be checked", action)
	}
	wantHint := tui.KeyHint(want, "to select")
	if !strings.Contains(wantHint, want) || !strings.Contains(wantHint, "to select") {
		t.Fatalf("tui.KeyHint = %q, want it to hold %q and the description", wantHint, want)
	}
	type hints interface {
		KeyText(string) string
		KeyHint(string, string) string
	}
	for name, h := range map[string]hints{"ToolRenderContext": ToolRenderContext{}, "MessageRenderOptions": MessageRenderOptions{}, "EntryRenderOptions": EntryRenderOptions{}} {
		if got := h.KeyText(action); got != want {
			t.Errorf("%s.KeyText(%q) = %q, want %q", name, action, got, want)
		}
		if got := h.KeyHint(action, "to select"); got != wantHint {
			t.Errorf("%s.KeyHint(%q) = %q, want %q", name, action, got, wantHint)
		}
		if got := h.KeyText("no.such.action"); got != "" {
			t.Errorf("%s.KeyText of an unbound action = %q, want empty", name, got)
		}
	}
}
