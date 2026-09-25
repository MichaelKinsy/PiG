package tui

// countdown_timer.go: reusable countdown timer.
//
// Ports upstream countdown-timer.ts (38 LOC).
//
// Unlike upstream which uses setInterval, pig exposes Start() which
// spawns a goroutine. The caller must call Dispose() to stop it.

import (
	"sync"
	"time"
)

// CountdownTimer counts down from a duration, calling OnTick each
// second and OnExpire when it reaches zero.
type CountdownTimer struct {
	remaining int // seconds
	onTick    func(seconds int)
	onExpire  func()
	mu        sync.Mutex
	stopCh    chan struct{}
	stopped   bool
}

// NewCountdownTimer creates a timer. It calls onTick immediately with
// the initial seconds value, then starts a 1-second ticker.
func NewCountdownTimer(timeout time.Duration, onTick func(int), onExpire func()) *CountdownTimer {
	ct := &CountdownTimer{
		remaining: int((timeout + 999*time.Millisecond) / time.Second), // ceil
		onTick:    onTick,
		onExpire:  onExpire,
		stopCh:    make(chan struct{}),
	}
	onTick(ct.remaining)
	go ct.run()
	return ct
}

func (ct *CountdownTimer) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ct.stopCh:
			return
		case <-ticker.C:
			ct.mu.Lock()
			ct.remaining--
			r := ct.remaining
			ct.mu.Unlock()
			ct.onTick(r)
			if r <= 0 {
				ct.Dispose()
				ct.onExpire()
				return
			}
		}
	}
}

// Dispose stops the timer.
func (ct *CountdownTimer) Dispose() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	if !ct.stopped {
		ct.stopped = true
		close(ct.stopCh)
	}
}
