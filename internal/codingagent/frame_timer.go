package codingagent

import "time"

// frameTimer keeps the deadline of the next frame for the owner loop (installRenderDispatcher), as the timer behind Pi's one setTimeout does for its
// event loop. A tick is a wake, not a promise that the armed deadline has passed: the renderer checks the frame's due time itself (tui RunDueRender).
// The runtime's timer is the default (runtimeFrameTimer); newKernelFrameTimer gives a more exact one where the system has one (frame_timer_linux.go).
type frameTimer interface {
	// C is the channel the ticks arrive on, the same one for the life of the timer.
	C() <-chan time.Time
	// Reset arms the timer to tick once the given time has passed, replacing a deadline armed before.
	Reset(after time.Duration)
	// Stop disarms the timer; no tick of the deadline it held arrives afterwards.
	Stop()
	// Close releases what the timer holds. A Reset or Stop that comes after it is harmless.
	Close()
}

// runtimeFrameTimer is the Go runtime's timer: the default, and the one whose time a testing/synctest bubble controls.
type runtimeFrameTimer struct{ timer *time.Timer }

func newRuntimeFrameTimer() frameTimer {
	// A stopped timer: a channel-based timer delivers no tick of a deadline that Stop or Reset replaced.
	timer := time.NewTimer(0)
	timer.Stop()
	return runtimeFrameTimer{timer}
}

func (r runtimeFrameTimer) C() <-chan time.Time       { return r.timer.C }
func (r runtimeFrameTimer) Reset(after time.Duration) { r.timer.Reset(max(after, 0)) }
func (r runtimeFrameTimer) Stop()                     { r.timer.Stop() }
func (r runtimeFrameTimer) Close()                    { r.timer.Stop() }
