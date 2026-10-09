package ai

import (
	"context"
	"math"
	"time"
)

// nodeTimerMaxMs is Node's TIMEOUT_MAX, the largest delay setTimeout accepts.
const nodeTimerMaxMs = 1<<31 - 1

// nodeTimerFallbackMs is the delay Node's setTimeout substitutes for one it cannot honour (lib/internal/timers.js Timeout: "after = 1").
const nodeTimerFallbackMs = 1

// nodeTimerDuration is the delay Node's setTimeout actually waits for ms: fractions truncate, and a value that is NaN, below 1 or above 2^31-1 waits 1ms.
func nodeTimerDuration(ms float64) time.Duration {
	if math.IsNaN(ms) || ms < 1 || ms > nodeTimerMaxMs {
		return nodeTimerFallbackMs * time.Millisecond
	}
	return time.Duration(math.Trunc(ms)) * time.Millisecond
}

// Sleep waits ms milliseconds as Node's setTimeout would, or until ctx is cancelled. A context that is already cancelled fails at once, even for a zero delay, and a cancellation during the wait stops the timer. Both return the cancellation cause, as the upstream promise rejects with signal.reason.
//
// upstream: packages/ai/src/utils/sleep.ts:1
func Sleep(ctx context.Context, ms float64) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	timer := time.NewTimer(nodeTimerDuration(ms))
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// CombinedAbortSignal is the result of [CombineAbortSignals]. Signal is nil when no signal was supplied. Cleanup releases the listeners the combination installed.
//
// upstream: packages/ai/src/utils/abort-signals.ts:1
type CombinedAbortSignal struct {
	Signal  context.Context
	Cleanup func()

	// abort is the combination's own cancel function; the supplied signals call it.
	abort context.CancelCauseFunc
}

// CombineAbortSignals returns one signal that aborts when any supplied signal aborts, with that signal's cause. Nil signals are ignored, no signal gives a nil Signal, and a single signal is returned as it is. A signal that is already cancelled cancels the combination before it is returned, and the signals after it are not observed. Cleanup stops observing the supplied signals. A Go context has no synchronous abort listener, so the combination follows a supplied signal one goroutine hop after that signal is cancelled; wait on Signal.Done rather than reading Err straight after the cancel.
//
// upstream: packages/ai/src/utils/abort-signals.ts:6
func CombineAbortSignals(signals ...context.Context) CombinedAbortSignal {
	var active []context.Context
	for _, signal := range signals {
		if signal != nil {
			active = append(active, signal)
		}
	}
	switch len(active) {
	case 0:
		return CombinedAbortSignal{Cleanup: func() {}}
	case 1:
		return CombinedAbortSignal{Signal: active[0], Cleanup: func() {}}
	}
	combined, abort := context.WithCancelCause(context.Background())
	var stops []func() bool
	for _, signal := range active {
		if signal.Err() != nil {
			abort(context.Cause(signal))
			break
		}
		stops = append(stops, context.AfterFunc(signal, func() { abort(context.Cause(signal)) }))
	}
	return CombinedAbortSignal{Signal: combined, abort: abort, Cleanup: func() {
		for _, stop := range stops {
			stop()
		}
	}}
}
