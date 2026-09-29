package tui

// countdown_timer.go: reusable countdown timer.
//
// Ports upstream countdown-timer.ts (38 LOC).

import (
	"sync"
	"time"
)

// CountdownTimer counts down from a duration, calling OnTick each
// second and OnExpire when it reaches zero.
type CountdownTimer struct {
	remaining int // seconds
	dispatch  func(func())
	onTick    func(seconds int)
	onExpire  func()
	mu        sync.Mutex
	stopCh    chan struct{}
	stopped   bool
}

// NewCountdownTimer creates a timer. It calls onTick immediately with
// the initial seconds value, then starts a 1-second ticker.
//
// Upstream's interval callback runs on the event loop that owns the dialog, so
// dispatch runs each second's decrement, onTick and expiry together on the
// owner loop. A nil dispatch runs them on the ticker goroutine. Dispose stops
// every later second, including one already dispatched.
func NewCountdownTimer(timeout time.Duration, dispatch func(func()), onTick func(int), onExpire func()) *CountdownTimer {
	ct := &CountdownTimer{
		remaining: int((timeout + 999*time.Millisecond) / time.Second), // ceil
		dispatch:  dispatch,
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
			if ct.dispatch == nil {
				if ct.tick() {
					return
				}
				continue
			}
			select {
			case <-ct.stopCh:
				return
			default:
			}
			ct.dispatch(func() { ct.tick() })
		}
	}
}

// tick is one run of upstream's interval callback. It reports whether the
// countdown has ended.
func (ct *CountdownTimer) tick() bool {
	ct.mu.Lock()
	if ct.stopped {
		ct.mu.Unlock()
		return true
	}
	ct.remaining--
	r := ct.remaining
	ct.mu.Unlock()
	ct.onTick(r)
	if r > 0 {
		return false
	}
	ct.Dispose()
	ct.onExpire()
	return true
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
