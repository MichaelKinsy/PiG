package tui

import (
	"strings"
	"sync"
	"testing"
)

type ifaceOutput struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *ifaceOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *ifaceOutput) take() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sb.String()
	b.sb.Reset()
	return s
}

// Pi's ProcessTerminal writes these exact sequences (packages/tui/src/terminal.ts moveBy, hideCursor, showCursor,
// clearLine, clearFromCursor, clearScreen, setTitle, setProgress). The test drives them through the Terminal
// interface so a custom Terminal replaces ProcessTerminal without changing the contract.
// mutation-checked: zeroing the results of Terminal.ClearScreen, Terminal.HideCursor, Terminal.MoveBy, Terminal.SetProgress, Terminal.SetTitle, Terminal.ShowCursor, Terminal.Write fails it
// Pi: packages/tui/src/terminal.ts:100 (hideCursor)
// Pi: packages/tui/src/terminal.ts:97 (moveBy)
// Pi: packages/tui/src/terminal.ts:112 (setProgress)
// Pi: packages/tui/src/terminal.ts:109 (setTitle)
// Pi: packages/tui/src/terminal.ts:101 (showCursor)
// Pi: packages/tui/src/terminal.ts:87 (write)
// Pi's ProcessTerminal writes these exact sequences (packages/tui/src/terminal.ts: write terminal.ts:477, moveBy
// terminal.ts:496, hideCursor terminal.ts:507, showCursor terminal.ts:511, clearLine terminal.ts:515, clearFromCursor
// terminal.ts:519, clearScreen terminal.ts:523, setTitle terminal.ts:527, setProgress terminal.ts:532). The test drives them
// through the Terminal interface so a custom Terminal replaces ProcessTerminal without changing the contract.
// packages/tui/src/terminal.ts:97,101,109,112 (Terminal.moveBy, showCursor, setTitle, setProgress) and 496-535 (ProcessTerminal).
func TestTerminalInterfaceOutputMatchesUpstreamSequences(t *testing.T) {
	out := &ifaceOutput{}
	var term Terminal = NewProcessTerminalWithOutput(nil, nil, out)
	cases := []struct {
		name string
		do   func()
		want string
	}{
		{"moveBy down", func() { term.MoveBy(3) }, "\x1b[3B"},
		{"moveBy up", func() { term.MoveBy(-2) }, "\x1b[2A"},
		{"moveBy zero", func() { term.MoveBy(0) }, ""},
		{"hideCursor", term.HideCursor, "\x1b[?25l"},
		{"showCursor", term.ShowCursor, "\x1b[?25h"},
		{"clearLine", term.ClearLine, "\x1b[K"},
		{"clearFromCursor", term.ClearFromCursor, "\x1b[J"},
		{"clearScreen", term.ClearScreen, "\x1b[2J\x1b[H"},
		{"setTitle", func() { term.SetTitle("pig · x") }, "\x1b]0;pig · x\x07"},
		{"write", func() { term.Write("raw\x1b[0m") }, "raw\x1b[0m"},
		{"setProgress true", func() { term.SetProgress(true) }, "\x1b]9;4;3\x07"},
		{"setProgress false", func() { term.SetProgress(false) }, "\x1b]9;4;0\x07"},
	}
	for _, tc := range cases {
		out.take()
		tc.do()
		if got := out.take(); got != tc.want {
			t.Errorf("%s wrote %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Pi's columns and rows read process.stdout, then COLUMNS and LINES, and fall back to 80 by 24 (packages/tui/src/terminal.ts:488,
// terminal.ts:492).
func TestTerminalInterfaceSizeFallsBackWithoutATerminal(t *testing.T) {
	var term Terminal = NewProcessTerminalWithOutput(nil, nil, &ifaceOutput{})
	t.Setenv("COLUMNS", "")
	t.Setenv("LINES", "")
	if term.Columns() != 80 || term.Rows() != 24 {
		t.Fatalf("size without a terminal = %dx%d, want 80x24", term.Columns(), term.Rows())
	}
	t.Setenv("COLUMNS", "132")
	t.Setenv("LINES", "40")
	if term.Columns() != 132 || term.Rows() != 40 {
		t.Fatalf("size from COLUMNS/LINES = %dx%d, want 132x40", term.Columns(), term.Rows())
	}
}

// mutation-checked: zeroing the results of Terminal.KittyProtocolActive fails it
// Pi: packages/tui/src/terminal.ts:94 (kittyProtocolActive)
// packages/tui/src/terminal.ts:94,166 (Terminal.kittyProtocolActive).
func TestTerminalInterfaceKittyProtocolActiveFollowsTheSessionState(t *testing.T) {
	var term Terminal = NewProcessTerminalWithOutput(nil, nil, &ifaceOutput{})
	t.Cleanup(func() { SetKittyProtocolActive(false) })
	SetKittyProtocolActive(false)
	if term.KittyProtocolActive() {
		t.Fatal("KittyProtocolActive = true before any protocol reply")
	}
	SetKittyProtocolActive(true)
	if !term.KittyProtocolActive() {
		t.Fatal("KittyProtocolActive = false after the protocol was enabled")
	}
}
