package tui

import (
	"io"
	"sync"
	"testing"
)

// Go-only: Pi's pi-tui runs on one event loop, so `requestRender` and `stop` never overlap. Here RequestRender is called from component goroutines (the
// Loader's animation, a spinner) while the owner loop stops the TUI, so `stopped` is an atomic (a plain bool is a data race under -race at RequestRender).
func TestRequestRenderFromAComponentGoroutineWhileTheOwnerStopsTheTUI(t *testing.T) {
	for range 50 {
		ui := NewTuiAltScreenWithOutput(io.Discard, 40, 10, TuiAltScreenOptions{})
		ui.SetRenderDispatcher(func(f func()) { f() })
		ui.Add(NewText("x"))
		ui.Start()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				ui.RequestRender()
			}
		}()
		ui.StopWithOptions(StopOptions{})
		wg.Wait()
	}
}
