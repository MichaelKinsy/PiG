package host

import (
	"context"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
)

// Ports packages/chord/src/next/host/sleep.ts. Waiting helpers for routines, jobs and handlers (§14): `sleep(ms, ctx)` and
// `untilAborted(ctx)`, which end with their context, leaving no timer or listener behind. Portable: timers and abort
// signals only.

// maxTimer is the longest delay one timer takes (a longer one fires at once).
const maxTimer = time.Duration(1<<31-1) * time.Millisecond

// systemLoop is the clock and timers of Sleep for app code: the process's, with no host section around the wake-up.
type systemLoop struct{}

func (systemLoop) Now() int64       { return time.Now().UnixMilli() }
func (systemLoop) Queue(job func()) { go job() }
func (systemLoop) AfterFunc(d time.Duration, job func()) Timer {
	return systemTimer{time.AfterFunc(nodeDelay(d), job)}
}

type systemTimer struct{ *time.Timer }

func (t systemTimer) Stop() bool { return t.Timer.Stop() }

// Sleep returns nil after ms (Pi's `sleep(ms, ctx)` resolving), or the abort reason once ctx is aborted
// (`context.Cause(ctx)`, Pi's rejection), clearing its timer. Delays above the timer limit (2^31-1 ms) are chained timers.
// Pi takes milliseconds; the delay is a time.Duration.
func Sleep(ms time.Duration, ctx context.Context) error { return sleepOn(systemLoop{}, ms, ctx) }

// sleepOn is Sleep on a loop's timers.
func sleepOn(loop eventLoop, ms time.Duration, ctx context.Context) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	woke := make(chan struct{})
	// timer is the timer of the running link of the chain; the wake-up (a timer job) and the abort (this goroutine) both
	// reach it.
	var mu sync.Mutex
	var timer Timer
	var wait func(left time.Duration)
	wait = func(left time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		timer = loop.AfterFunc(min(left, maxTimer), func() {
			if left > maxTimer {
				wait(left - maxTimer)
				return
			}
			close(woke)
		})
	}
	wait(ms)
	// The timer is made in the caller's synchronous part, as Pi's: what follows waits for it (or the abort).
	parking.Park(ctx)
	select {
	case <-woke:
		return nil
	case <-ctx.Done():
		mu.Lock()
		timer.Stop()
		mu.Unlock()
		return context.Cause(ctx)
	}
}

// UntilAborted returns once ctx is aborted (never without an abort signal: a context with no Done channel blocks for
// good, as Pi's promise never resolves); it leaves no listener behind.
func UntilAborted(ctx context.Context) {
	parking.Park(ctx)
	<-ctx.Done()
}
