package sync

import (
	"context"
	"sync"
	"time"
)

// Ports packages/chord/src/sync/client.ts (FlushScheduler)
//
// Async contract: Pi's scheduler arms a timer whose callback starts the flush promise, and `close()` awaits the flush
// underway and flushes once more. Here a timer's callback runs the flush on the goroutine the timer's own mechanism gives
// it (time.AfterFunc starts one per firing), so the scheduler starts none; `running` is closed when a flush ended, and Close
// waits on it. SetTimer and Now are injected, as Pi's, so tests drive time.

// FlushSchedulerOptions are what NewFlushScheduler takes; a zero duration takes its default.
type FlushSchedulerOptions struct {
	// Flush uploads what was committed. A failed flush is retried with backoff.
	Flush func() error
	// SetTimer arms a timer calling callback after delay (on a goroutine of its own, once) and returns its disarm.
	SetTimer func(callback func(), delay time.Duration) (disarm func())
	Now      func() time.Time
	// OnError is told of a failed flush; the scheduler retries it.
	OnError func(err error)
	// Interval is the delay of an ordinary flush after a commit. Default 5 seconds.
	Interval time.Duration
	// Urgent is the delay of a flush after an urgent commit. Default 200 milliseconds.
	Urgent time.Duration
	// Retry is the first retry delay after a failed flush, doubling up to MaxRetry. Default 1 second.
	Retry time.Duration
	// MaxRetry bounds the retry delay. Default 1 minute.
	MaxRetry time.Duration
}

// RealTimers is SetTimer over time.AfterFunc and Now over time.Now.
func RealTimers() (setTimer func(callback func(), delay time.Duration) (disarm func()), now func() time.Time) {
	return func(callback func(), delay time.Duration) func() {
		timer := time.AfterFunc(delay, callback)
		return func() { timer.Stop() }
	}, time.Now
}

// FlushScheduler decides when to flush: some time after a commit, sooner after an urgent one, one flush at a time.
// Commits that arrive during a flush keep their urgency and schedule the next one when it ends. A failed flush is retried
// with backoff until one succeeds, so the last commits of a turn do not wait for the next commit.
type FlushScheduler struct {
	options FlushSchedulerOptions

	mu     sync.Mutex
	due    *time.Time
	disarm func()
	// running is closed when the flush underway ended; nil when none is.
	running chan struct{}
	// pendingDue is when the commits that arrived during a flush want the next one.
	pendingDue *time.Time
	failures   int
	closed     bool
}

// NewFlushScheduler is a scheduler of options.
func NewFlushScheduler(options FlushSchedulerOptions) *FlushScheduler {
	return &FlushScheduler{options: options}
}

func orDefault(value, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return value
}

// Notify: a commit landed.
func (scheduler *FlushScheduler) Notify(urgent bool) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if scheduler.closed {
		return
	}
	delay := orDefault(scheduler.options.Interval, 5*time.Second)
	if urgent {
		delay = orDefault(scheduler.options.Urgent, 200*time.Millisecond)
	}
	due := scheduler.options.Now().Add(delay)
	if scheduler.running != nil {
		if scheduler.pendingDue == nil || due.Before(*scheduler.pendingDue) {
			scheduler.pendingDue = &due
		}
		return
	}
	scheduler.arm(due)
}

// Close flushes now, after a flush underway, and stops scheduling. A failure is returned. ctx ends only the wait for a
// flush underway.
func (scheduler *FlushScheduler) Close(ctx context.Context) error {
	scheduler.mu.Lock()
	scheduler.closed = true
	if scheduler.disarm != nil {
		scheduler.disarm()
		scheduler.disarm = nil
	}
	scheduler.due = nil
	running := scheduler.running
	scheduler.mu.Unlock()
	if running != nil {
		select {
		case <-running:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	return scheduler.options.Flush()
}

// arm arms the timer for due unless one is armed for sooner. The caller holds mu.
func (scheduler *FlushScheduler) arm(due time.Time) {
	if scheduler.due != nil && !scheduler.due.After(due) {
		return
	}
	if scheduler.disarm != nil {
		scheduler.disarm()
	}
	scheduler.due = &due
	scheduler.disarm = scheduler.options.SetTimer(scheduler.run, max(0, due.Sub(scheduler.options.Now())))
}

// run is the timer's callback: one flush, then the next one when commits arrived during it or it failed.
func (scheduler *FlushScheduler) run() {
	scheduler.mu.Lock()
	// A timer that fired as Close ran cannot be stopped: its callback does nothing after close, as Pi's never runs.
	if scheduler.closed {
		scheduler.mu.Unlock()
		return
	}
	scheduler.disarm = nil
	scheduler.due = nil
	running := make(chan struct{})
	scheduler.running = running
	scheduler.mu.Unlock()

	err := scheduler.options.Flush()
	if err != nil {
		scheduler.options.OnError(err)
	}

	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	var retry *time.Time
	if err != nil {
		maxDelay := orDefault(scheduler.options.MaxRetry, time.Minute)
		delay := maxDelay
		if scheduler.failures < 32 {
			delay = min(orDefault(scheduler.options.Retry, time.Second)<<scheduler.failures, maxDelay)
		}
		scheduler.failures++
		at := scheduler.options.Now().Add(delay)
		retry = &at
	} else {
		scheduler.failures = 0
	}
	scheduler.running = nil
	close(running)
	if scheduler.closed {
		return
	}
	pending := scheduler.pendingDue
	scheduler.pendingDue = nil
	// A retry waits for its backoff even when commits arrived: they flush with it.
	due := retry
	if due == nil {
		due = pending
	}
	if due != nil {
		scheduler.arm(*due)
	}
}
