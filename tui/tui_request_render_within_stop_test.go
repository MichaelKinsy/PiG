package tui

import (
	"io"
	"sync"
	"testing"
	"time"
)

// Go-only, as TestRequestRenderFromAComponentGoroutineWhileTheOwnerStopsTheTUI: a Loader's animation step asks for its
// frame through RequestRenderWithin from its own goroutine, and the timer it arms fires on another, while the owner loop
// stops and starts the TUI. Both read `stopped`, which Start writes outside the render lock: an atomic (run with -race).
func TestRequestRenderWithinFromAComponentGoroutineWhileTheOwnerStopsAndStartsTheTUI(t *testing.T) {
	for range 50 {
		ui := NewWithOutput(io.Discard, 40, 10)
		ui.SetRenderDispatcher(func(f func()) { f() })
		ui.Add(NewText("x"))
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 200 {
				ui.RequestRenderWithin(time.Microsecond)
			}
		})
		for range 4 {
			ui.StopWithOptions(StopOptions{})
			ui.Start()
		}
		wg.Wait()
		ui.StopWithOptions(StopOptions{})
	}
}
