//go:build unix

package tui

import (
	"testing"

	"golang.org/x/term"
)

// A stop for an external editor or a suspend ends and the terminal starts again: the transaction reopens. A signal's sealed stop is the process exit's, but a
// restart (raw mode entered again) must find the foreign writers working.
func TestEnteringRawModeReopensTheTerminalASealedStopHandedBack(t *testing.T) {
	terminal, out := interruptedRawModeTerminal(t)
	savedRaw, savedRestore := makeRawTerminal, restoreTerminal
	t.Cleanup(func() { makeRawTerminal, restoreTerminal = savedRaw, savedRestore })
	makeRawTerminal = func(int) (*term.State, error) { return &term.State{}, nil }
	restoreTerminal = func(int, *term.State) error { return nil }

	tx := terminal.BeginStop()
	tx.End(true)
	terminal.SetTitle("T")
	if got := out.String(); got != "" {
		t.Fatalf("a title written to the sealed terminal: %q", got)
	}
	restore, err := terminal.enterRawMode(false)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	out.Reset()
	terminal.SetTitle("T")
	if got, want := out.String(), stopTitleSeq; got != want {
		t.Fatalf("after the terminal started again a title wrote %q, want %q", got, want)
	}
}
