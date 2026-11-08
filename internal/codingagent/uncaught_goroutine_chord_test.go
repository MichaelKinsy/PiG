package codingagent

import (
	"errors"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
)

// A failure no Chord error reporter handled reaches the interactive uncaught handler, as Pi's rethrow of such a failure
// reaches interactive-mode.ts's uncaughtException handler (uncaughtCrash); removing the handler restores the previous
// receiver. Chord's reporter is the receiver: the test reports through the one the handler installed.
func TestChordUncaughtFailuresReachTheInteractiveUncaughtHandler(t *testing.T) {
	failure := errors.New("listener failed")
	outer := make(chan error, 1)
	original := chord.SetUncaughtErrorReporter(func(err error) { outer <- err })
	t.Cleanup(func() { chord.SetUncaughtErrorReporter(original) })
	got := make(chan any, 1)
	restore := SetUncaughtGoroutineHandler(func(value any, _ []byte) { got <- value })
	// installed is the reporter SetUncaughtGoroutineHandler put in place; swapping it out and back is how to call it.
	installed := chord.SetUncaughtErrorReporter(func(error) {})
	chord.SetUncaughtErrorReporter(installed)
	installed(failure)
	select {
	case value := <-got:
		if !errors.Is(value.(error), failure) {
			t.Fatalf("handler got %v", value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the uncaught handler never ran")
	}
	restore()
	previous := chord.SetUncaughtErrorReporter(func(error) {})
	chord.SetUncaughtErrorReporter(previous)
	previous(failure)
	select {
	case err := <-outer:
		if !errors.Is(err, failure) {
			t.Fatalf("previous receiver got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the previous receiver was not restored")
	}
	select {
	case value := <-got:
		t.Fatalf("handler ran after restore: %v", value)
	default:
	}
}
