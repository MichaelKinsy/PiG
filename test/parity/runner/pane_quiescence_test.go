//go:build parity

package runner

import (
	"strconv"
	"testing"
	"time"
)

// A rain frame that stops changing for less than the window and then moves
// again must restart the window: the quiet gap before a late drop appears is
// not completion.
func TestWaitForPaneQuiescentRestartsWindowOnChange(t *testing.T) {
	const window = 120 * time.Millisecond
	start := time.Now()
	var lastChange time.Time
	value := 0
	sample := func() string {
		// One brief quiet gap (shorter than the window), then a late change, then quiet.
		elapsed := time.Since(start)
		switch {
		case elapsed < 30*time.Millisecond:
			value = 0
		case elapsed < 90*time.Millisecond:
			value = 1
		default:
			value = 2
		}
		if value == 2 && lastChange.IsZero() {
			lastChange = time.Now()
		}
		return strconv.Itoa(value)
	}
	if !waitForPaneQuiescent(t.Context(), sample, window, 5*time.Millisecond, 5*time.Second) {
		t.Fatal("quiescence was not reached")
	}
	if got := time.Since(lastChange); got < window {
		t.Fatalf("returned %s after the last change, want at least the %s window", got, window)
	}
}

func TestWaitForPaneQuiescentTimesOutWhileStateKeepsChanging(t *testing.T) {
	n := 0
	sample := func() string { n++; return strconv.Itoa(n) }
	if waitForPaneQuiescent(t.Context(), sample, 50*time.Millisecond, time.Millisecond, 200*time.Millisecond) {
		t.Fatal("a continuously changing pane reported quiescence")
	}
}
