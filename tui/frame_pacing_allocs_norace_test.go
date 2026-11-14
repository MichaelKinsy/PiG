// SPDX-FileCopyrightText: Copyright Michael Kinsy
// SPDX-License-Identifier: MIT

//go:build !race

package tui

import (
	"testing"
	"time"
)

// scheduleFrames asks the scheduler for the next frame n times with nothing to run it: the question the hook answers, and the timer it arms.
func scheduleFrames(s *pacingScreen, n int) {
	ui := s.ui
	for range n {
		ui.mu.Lock()
		ui.renderTimer, ui.renderRequested = nil, true
		ui.scheduleRenderLocked()
		ui.mu.Unlock()
	}
}

func newSchedulingScreen(tb testing.TB, adaptive bool) *pacingScreen {
	s := newPacingScreen(tb.(*testing.T))
	if !adaptive {
		s.ui.SetFramePacing(nil)
	}
	s.frame(time.Millisecond, 0)
	s.ui.afterFunc = func(time.Duration, func()) stoppableTimer { return noopTimer{} }
	return s
}

// The policy's answer allocates nothing, so asking for a frame allocates under the policy what it allocates without it: the timer's callback.
func TestSchedulingAFrameAllocatesTheSameUnderEitherPolicy(t *testing.T) {
	var scheduling [2]float64
	for i, adaptive := range []bool{false, true} {
		s := newSchedulingScreen(t, adaptive)
		if allocs := testing.AllocsPerRun(1000, func() { _ = s.interval() }); allocs != 0 {
			t.Errorf("adaptive=%v: the interval allocates %v times a call, want 0", adaptive, allocs)
		}
		scheduling[i] = testing.AllocsPerRun(1000, func() { scheduleFrames(s, 1) })
	}
	if scheduling[0] != scheduling[1] {
		t.Errorf("scheduling a frame allocates %v times with Pi's interval and %v times with the policy, want the same", scheduling[0], scheduling[1])
	}
}
