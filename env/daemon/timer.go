// SPDX-License-Identifier: MIT

package daemon

import "time"

// waitTimer is one timer that a wait loop re-arms on every pass, in place of a time.After or time.NewTimer per pass. The module's Go version gives Reset and Stop their synchronous channel semantics, so a re-armed timer never delivers a tick from an earlier arming.
type waitTimer struct {
	timer *time.Timer
}

// arm starts or restarts the timer to fire after d and returns its channel.
func (w *waitTimer) arm(d time.Duration) <-chan time.Time {
	if w.timer == nil {
		w.timer = time.NewTimer(d)
	} else {
		w.timer.Reset(d)
	}
	return w.timer.C
}

// stop releases the timer. It is safe on a timer that was never armed.
func (w *waitTimer) stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}
