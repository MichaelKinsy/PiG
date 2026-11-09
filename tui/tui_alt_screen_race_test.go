package tui

import (
	"bytes"
	"sync"
	"testing"
)

// A component's own goroutine asks for a render (the Loader's animation ticks do) while the client's close stops the renderer: in Pi both run on
// one thread, here they are goroutines, so the stop flag RequestRender reads and Stop writes must not be a plain bool (run with -race).
func TestARenderRequestFromAComponentGoroutineWhileTheAltScreenStops(t *testing.T) {
	for range 20 {
		tui := newAltScreenForTest(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
		tui.Add(NewText("row"))
		tui.Start()
		var requesters sync.WaitGroup
		started := make(chan struct{})
		for range 4 {
			requesters.Go(func() {
				<-started
				for range 50 {
					tui.RequestRender()
				}
			})
		}
		close(started)
		tui.StopWithOptions(StopOptions{})
		requesters.Wait()
	}
}
