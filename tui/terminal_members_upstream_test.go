package tui

import (
	"bytes"
	"os"
	"testing"
)

// The Terminal interface mirrors upstream's Terminal (packages/tui/src/terminal.ts:63-120). Each member is driven through the
// interface, as TUI drives it, and its terminal effect is asserted.
// mutation-checked: zeroing the results of Terminal.ClearFromCursor, Terminal.ClearLine fails it
func TestTerminalInterfaceControlSequences(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	var out bytes.Buffer
	var term Terminal = NewProcessTerminalWithOutput(nil, stdout, &out)

	cases := []struct {
		name string
		call func()
		want string
	}{
		{"clearLine", term.ClearLine, "\x1b[K"},             // terminal.ts clearLine: "\x1b[K"
		{"clearFromCursor", term.ClearFromCursor, "\x1b[J"}, // terminal.ts clearFromCursor: "\x1b[J"
		{"clearScreen", term.ClearScreen, "\x1b[2J\x1b[H"},  // terminal.ts clearScreen: "\x1b[2J\x1b[H"
	}
	for _, tc := range cases {
		out.Reset()
		tc.call()
		if got := out.String(); got != tc.want {
			t.Errorf("%s wrote %q, want %q", tc.name, got, tc.want)
		}
	}
}

// terminal.ts:488 `get columns()` is process.stdout.columns || Number(process.env.COLUMNS) || 80.
func TestTerminalColumnsFallsBackToEnvironmentThenEighty(t *testing.T) {
	stdout, err := os.CreateTemp(t.TempDir(), "stdout") // a regular file has no terminal size
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	var term Terminal = NewProcessTerminalWithOutput(nil, stdout, nil)

	t.Setenv("COLUMNS", "132")
	if got := term.Columns(); got != 132 {
		t.Errorf("Columns with COLUMNS=132 = %d, want 132", got)
	}
	t.Setenv("COLUMNS", "not-a-number")
	if got := term.Columns(); got != 80 {
		t.Errorf("Columns with an invalid COLUMNS = %d, want 80", got)
	}
	t.Setenv("COLUMNS", "")
	if got := term.Columns(); got != 80 {
		t.Errorf("Columns without COLUMNS = %d, want 80", got)
	}
}
