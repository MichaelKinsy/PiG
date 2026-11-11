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

// FlushSchedulerOptions are what NewFlushScheduler takes. Times are milliseconds, as Pi's (`delayMs`, `intervalMs`, ...); a
// zero delay takes its default, where Pi's `??` takes an explicit zero as it is.
type FlushSchedulerOptions struct {
	// Flush uploads what was committed. A failed flush is retried with backoff.
	Flush func() error
	// SetTimer arms a timer calling callback after delayMs (on a goroutine of its own, once) and returns its disarm.
	SetTimer func(callback func(), delayMs int64) (disarm func())
	// Now is the time in milliseconds, on any clock that does not run backwards.
	Now func() int64
	// OnError is told of a failed flush; the scheduler retries it.
	OnError func(err error)
	// IntervalMs is the delay of an ordinary flush after a commit. Default 5 seconds.
	IntervalMs int64
	// UrgentMs is the delay of a flush after an urgent commit. Default 200 milliseconds.
	UrgentMs int64
	// RetryMs is the first retry delay after a failed flush, doubling up to MaxRetryMs. Default 1 second.
	RetryMs int64
	// MaxRetryMs bounds the retry delay. Default 1 minute.
	MaxRetryMs int64
}

// RealTimers is SetTimer over time.AfterFunc and Now over the wall clock in milliseconds.
func RealTimers() (setTimer func(callback func(), delayMs int64) (disarm func()), now func() int64) {
	return func(callback func(), delayMs int64) func() {
		timer := time.AfterFunc(time.Duration(delayMs)*time.Millisecond, callback)
		return func() { timer.Stop() }
	}, func() int64 { return time.Now().UnixMilli() }
}

// FlushScheduler decides when to flush: some time after a commit, sooner after an urgent one, one flush at a time.
// Commits that arrive during a flush keep their urgency and schedule the next one when it ends. A failed flush is retried
// with backoff until one succeeds, so the last commits of a turn do not wait for the next commit.
type FlushScheduler struct {
	options FlushSchedulerOptions

	mu     sync.Mutex
	due    *int64
	disarm func()
	// running is closed when the flush underway ended; nil when none is.
	running chan struct{}
	// pendingDue is when the commits that arrived during a flush want the next one.
	pendingDue *int64
	failures   int
	closed     bool
}

// NewFlushScheduler is a scheduler of options.
func NewFlushScheduler(options FlushSchedulerOptions) *FlushScheduler {
	return &FlushScheduler{options: options}
}

func orDefault(value, fallback int64) int64 {
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
	delay := orDefault(scheduler.options.IntervalMs, 5000)
	if urgent {
		delay = orDefault(scheduler.options.UrgentMs, 200)
	}
	due := scheduler.options.Now() + delay
	if scheduler.running != nil {
		if scheduler.pendingDue == nil || due < *scheduler.pendingDue {
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
func (scheduler *FlushScheduler) arm(due int64) {
	if scheduler.due != nil && *scheduler.due <= due {
		return
	}
	if scheduler.disarm != nil {
		scheduler.disarm()
	}
	scheduler.due = &due
	scheduler.disarm = scheduler.options.SetTimer(scheduler.run, max(0, due-scheduler.options.Now()))
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
	var retry *int64
	if err != nil {
		maxDelay := orDefault(scheduler.options.MaxRetryMs, 60_000)
		delay := maxDelay
		if scheduler.failures < 32 {
			delay = min(orDefault(scheduler.options.RetryMs, 1000)<<scheduler.failures, maxDelay)
		}
		scheduler.failures++
		at := scheduler.options.Now() + delay
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
