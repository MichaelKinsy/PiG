package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// filterTerminalInput runs data through the extension shortcut and the
// in-process terminal-input listeners on the production pass and reports what
// normal handling would receive.
func (m *InteractiveMode) filterTerminalInput(data string) (string, bool) {
	passed, consumed := "", true
	_ = m.passTerminalInput(context.Background(), data, nil, func(_ context.Context, data string) error {
		passed, consumed = data, false
		return nil
	})
	return passed, consumed
}

// notifyTerminalInput reports whether the shortcut or a listener consumed data.
func (m *InteractiveMode) notifyTerminalInput(data string) bool {
	_, consumed := m.filterTerminalInput(data)
	return consumed
}

// Upstream tui.ts handleTerminalInput: a listener's `data` replaces the input
// for later listeners and for normal handling, a consuming listener ends
// dispatch, and input rewritten to empty is dropped.
func TestTerminalInputListenersRewriteInput(t *testing.T) {
	m := &InteractiveMode{}
	ui := &ExtUIContext{m: m}
	rewrite := func(to string) *string { return &to }
	var seenBySecond []string
	ui.OnTerminalInput(func(data string) extension.TerminalInputResult {
		switch data {
		case "j":
			return extension.TerminalInputResult{Data: rewrite("\x1b")}
		case "x":
			return extension.TerminalInputResult{Data: rewrite("")}
		}
		return extension.TerminalInputResult{}
	})
	ui.OnTerminalInput(func(data string) extension.TerminalInputResult {
		seenBySecond = append(seenBySecond, data)
		return extension.TerminalInputResult{Consume: data == "q"}
	})

	if got, consumed := m.filterTerminalInput("j"); consumed || got != "\x1b" {
		t.Fatalf("rewritten input = %q consumed=%v, want \\x1b delivered", got, consumed)
	}
	if got, consumed := m.filterTerminalInput("a"); consumed || got != "a" {
		t.Fatalf("plain input = %q consumed=%v, want a delivered", got, consumed)
	}
	if _, consumed := m.filterTerminalInput("x"); !consumed {
		t.Fatal("input rewritten to empty was delivered")
	}
	if _, consumed := m.filterTerminalInput("q"); !consumed {
		t.Fatal("consumed input was delivered")
	}
	want := []string{"\x1b", "a", "", "q"}
	if len(seenBySecond) != len(want) {
		t.Fatalf("second listener saw %q, want %q", seenBySecond, want)
	}
	for i := range want {
		if seenBySecond[i] != want[i] {
			t.Fatalf("second listener saw %q, want %q", seenBySecond, want)
		}
	}
}
