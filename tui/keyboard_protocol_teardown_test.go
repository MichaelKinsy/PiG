package tui

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// resetKeyboardProtocolState puts the package globals back to a known state so
// each case starts from "pig has pushed the flags".
func resetKeyboardProtocolState(pushed, modify, kitty bool) {
	keyboardProtocolPushed.Store(pushed)
	modifyOtherKeysActive.Store(modify)
	kittyProtocolActive.Store(kitty)
}

// TestDisableKeyboardProtocolIsIdempotentAndSingleWrite pins the two properties
// that let one owner handle teardown from several call paths.
//
// The pop must happen exactly once however many callers ask, because a second
// \x1b[<u pops an entry pig never pushed and disturbs whatever the terminal had
// underneath. It must also leave the terminal in one write, so a concurrent
// render cannot land between the Kitty pop and the modifyOtherKeys disable.
func TestDisableKeyboardProtocolIsIdempotentAndSingleWrite(t *testing.T) {
	var out bytes.Buffer
	term := NewProcessTerminalWithOutput(nil, nil, &out)
	resetKeyboardProtocolState(true, true, true)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

	term.disableKeyboardProtocol()
	first := out.String()

	if got := strings.Count(first, keyboardProtocolPop); got != 1 {
		t.Errorf("expected exactly one Kitty pop, got %d in %q", got, first)
	}
	if !strings.Contains(first, modifyOtherKeysDisable) {
		t.Errorf("modifyOtherKeys was left enabled: %q", first)
	}
	if IsKittyProtocolActive() {
		t.Error("kitty protocol still marked active after disable")
	}

	// One write, so the sequences cannot be split by a concurrent render.
	if want := keyboardProtocolPop + modifyOtherKeysDisable; first != want {
		t.Errorf("teardown was not a single contiguous sequence:\n got %q\nwant %q", first, want)
	}

	out.Reset()
	term.disableKeyboardProtocol()
	if second := out.String(); second != "" {
		t.Errorf("second call wrote %q; the pop must not repeat", second)
	}
}

// TestDrainInputDisablesProtocolBeforeDraining pins the ordering the drain
// depends on. A drain that runs while the terminal still reports key events has
// no stable end: releasing keys pressed during the drain produces more input,
// and whatever arrives after the window leaks into the user's shell.
func TestDrainInputDisablesProtocolBeforeDraining(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	var out bytes.Buffer
	term := NewProcessTerminalWithOutput(r, nil, &out)
	resetKeyboardProtocolState(true, false, true)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

	if err := term.DrainInput(100*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !strings.Contains(out.String(), keyboardProtocolPop) {
		t.Errorf("DrainInput drained without disabling the protocol first; wrote %q", out.String())
	}
	if keyboardProtocolPushed.Load() {
		t.Error("keyboardProtocolPushed still set after DrainInput")
	}
}

// TestRestoreTerminalFromSignalWritesNothingWithoutState covers the guard on
// the signal exit path: with no captured cooked state there is nothing to hand
// back, and the function must not emit escape sequences into a terminal it
// never took over.
//
// The positive case: that a real signal exit pops the Kitty flags: is not
// asserted here on purpose. Reaching it needs a live controlling terminal, and
// driving term.Restore against the developer's own tty from a unit test
// corrupts the session running it. The disable itself is covered by
// TestDisableKeyboardProtocolIsIdempotentAndSingleWrite, and the signal path
// shares that one owner.
func TestRestoreTerminalFromSignalWritesNothingWithoutState(t *testing.T) {
	var out bytes.Buffer
	saved := processTerminal
	processTerminal = NewProcessTerminalWithOutput(nil, nil, &out)
	t.Cleanup(func() { processTerminal = saved })

	savedState := cookedTerminalState.Load()
	cookedTerminalState.Store(nil)
	t.Cleanup(func() { cookedTerminalState.Store(savedState) })
	resetKeyboardProtocolState(true, false, true)
	t.Cleanup(func() { resetKeyboardProtocolState(false, false, false) })

	if RestoreTerminalFromSignal() {
		t.Error("reported a restore with no captured terminal state")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q with nothing to restore", out.String())
	}
	if !keyboardProtocolPushed.Load() {
		t.Error("cleared the pushed flag without restoring anything")
	}
}
