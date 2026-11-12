package tui

import (
	"io"
	"testing"
	"time"
)

// Go-only, as TestRequestRenderFromAComponentGoroutineWhileTheOwnerStopsTheTUI: RequestRender reads `stopped` before it
// takes the render lock, so the owner can stop the renderer between that read and the request. The test runs that order
// one step at a time: the stop, then the part of RequestRender that follows its read. The request must be dropped: a
// frame armed there is a timer that outlives the stop and, with a render dispatcher, a closure handed to an owner loop
// that may have ended.
func TestARenderRequestThatLosesTheRaceWithStopArmsNoFrame(t *testing.T) {
	mainScreen := NewWithOutput(io.Discard, 40, 10)
	altScreen := NewTuiAltScreenWithOutput(io.Discard, 40, 10, TuiAltScreenOptions{})
	surface := NewTuiSurfaceWithSize(newRecordingSession(), 40, 10, nil)
	for _, renderer := range []struct {
		name  string
		base  *tuiBase
		start func()
		stop  func(StopOptions)
	}{
		{"main screen", &mainScreen.tuiBase, mainScreen.Start, mainScreen.StopWithOptions},
		{"alternate screen", &altScreen.tuiBase, altScreen.Start, altScreen.StopWithOptions},
		{"surface", &surface.tuiBase, surface.Start, surface.StopWithOptions},
	} {
		for _, force := range []bool{false, true} {
			name := renderer.name + "/requested"
			if force {
				name = renderer.name + "/forced"
			}
			t.Run(name, func(t *testing.T) {
				base := renderer.base
				base.SetRenderDispatcher(func(func()) {})
				renderer.start()
				renderer.stop(StopOptions{PreserveScreen: true})
				base.mu.Lock()
				requestedAtStop := base.renderRequested
				base.mu.Unlock()
				armed := 0
				base.afterFunc = func(time.Duration, func()) stoppableTimer {
					armed++
					return &manualRenderTimer{}
				}
				base.requestRender(force)
				base.mu.Lock()
				timer, requested := base.renderTimer, base.renderRequested
				base.mu.Unlock()
				if armed != 0 || timer != nil || requested != requestedAtStop {
					t.Fatalf("a request after the stop armed %d timer(s), renderTimer set = %v, renderRequested = %v (%v at the stop); want the stop's state", armed, timer != nil, requested, requestedAtStop)
				}
			})
		}
	}
}
