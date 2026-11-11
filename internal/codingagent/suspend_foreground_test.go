package codingagent

import (
	"errors"
	"fmt"
	"reflect"
	"syscall"
	"testing"
	"time"
)

// pig divergence (D227): Pi's SIGCONT handler (interactive-mode.ts handleCtrlZ) calls ui.start() and requestRender(true) with
// no foreground check, so `bg` after Ctrl+Z re-enters raw mode from a background process group and the next terminal call
// stops it with SIGTTOU or SIGTTIN (#396). PiG parks until it owns the terminal.
func suspendForegroundOps(calls *[]string, foreground func() (bool, error), onContinue *func() error) suspendOperations {
	return suspendOperations{
		keepAlive:       func(time.Duration) func() { return func() { *calls = append(*calls, "clear-interval") } },
		ignoreInterrupt: func() func() { return func() { *calls = append(*calls, "remove-SIGINT") } },
		continued: func(continued func() error, _ func()) func() {
			*onContinue = continued
			return func() { *calls = append(*calls, "remove-SIGCONT") }
		},
		stop:          func() { *calls = append(*calls, "stop") },
		start:         func() error { *calls = append(*calls, "start"); return nil },
		requestRender: func() { *calls = append(*calls, "render-force") },
		kill:          func(pid int) error { *calls = append(*calls, fmt.Sprintf("kill:%d:SIGTSTP", pid)); return nil },
		foreground:    foreground,
	}
}

func TestSuspendedSessionResumedInTheBackgroundStaysParkedUntilFg(t *testing.T) {
	var calls []string
	var onContinue func() error
	owned := false
	ops := suspendForegroundOps(&calls, func() (bool, error) { return owned, nil }, &onContinue)
	if err := suspendTerminal(t.Context(), "linux", nil, ops); err != nil {
		t.Fatal(err)
	}
	// bg: SIGCONT reaches a group the shell's terminal does not belong to.
	calls = calls[:0]
	if err := onContinue(); !errors.Is(err, errSuspendParked) {
		t.Fatalf("a background SIGCONT returned %v, want errSuspendParked", err)
	}
	if want := []string{"kill:0:SIGTSTP"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("background SIGCONT calls=%q, want only the stop again %q: no raw mode, no frame, no cleanup", calls, want)
	}
	// fg: the shell gave the group the terminal, then SIGCONT.
	owned = true
	calls = calls[:0]
	if err := onContinue(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"clear-interval", "remove-SIGINT", "remove-SIGCONT", "start", "render-force"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("foreground SIGCONT calls=%q, want %q", calls, want)
	}
}

func TestSuspendedSessionWhoseTerminalIsGoneDoesNotRestart(t *testing.T) {
	var calls []string
	var onContinue func() error
	ops := suspendForegroundOps(&calls, func() (bool, error) { return false, syscall.EIO }, &onContinue)
	if err := suspendTerminal(t.Context(), "linux", nil, ops); err != nil {
		t.Fatal(err)
	}
	calls = calls[:0]
	if err := onContinue(); !errors.Is(err, syscall.EIO) {
		t.Fatalf("lost the terminal error: %v", err)
	}
	if want := []string{"clear-interval", "remove-SIGINT", "remove-SIGCONT"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%q, want the cleanup without a restart %q", calls, want)
	}
}
