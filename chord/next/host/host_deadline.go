package host

import "time"

// Ports packages/chord/src/next/host/deadline.ts. An activation's `readyTimeout` deadline (slice 6a §9): one timer that
// eager migration work pauses and resumes with the budget it had left, so a long migration does not fail its object while
// other activations keep their bounds.

// deadline is a deadline of a budget calling expired once when reached; paused time does not count.
type deadline struct {
	loop    eventLoop
	expired func()
	// remaining is the budget left when the timer last started (or paused).
	remaining time.Duration
	started   int64
	timer     Timer
	cleared   bool
}

// newDeadline starts a deadline of ms calling expired once when reached.
func newDeadline(loop eventLoop, ms time.Duration, expired func()) *deadline {
	d := &deadline{loop: loop, expired: expired, remaining: ms}
	d.start()
	return d
}

// pause stops the clock; resume restarts it with the budget left.
func (d *deadline) pause() {
	if d.timer == nil {
		return
	}
	d.timer.Stop()
	d.timer = nil
	d.remaining = max(0, d.remaining-time.Duration(d.loop.Now()-d.started)*time.Millisecond)
}

// resume restarts a paused clock.
func (d *deadline) resume() {
	if d.timer == nil && !d.cleared {
		d.start()
	}
}

// clear ends the deadline: expired is never called.
func (d *deadline) clear() {
	if d.timer != nil {
		d.timer.Stop()
	}
	d.timer = nil
	d.cleared = true
}

func (d *deadline) start() {
	d.started = d.loop.Now()
	d.timer = d.loop.AfterFunc(d.remaining, d.expired)
}
