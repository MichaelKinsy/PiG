package durable

import (
	"math"
	"sync"
	"time"
)

// Ports packages/durable/src/timers.ts.

// maxTimerDelay is the longest delay setTimeout supports; a longer one is waited for in several steps (timers.ts
// MAX_TIMER_DELAY).
const maxTimerDelay = 2_147_483_647

// NativeTimer is the default HarnessOptions.SetTimer: a Go timer, waiting out delays beyond setTimeout's limit in steps
// (timers.ts nativeTimer). Go timers never keep a process alive and cannot hold a Durable Object awake, so a background
// timer is never declined here.
func NativeTimer(callback func(), delayMs float64, background bool) (disarm func()) {
	var mu sync.Mutex
	var timer *time.Timer
	stopped := false
	var arm func(remaining float64)
	arm = func(remaining float64) {
		wait := setTimeoutDuration(math.Min(math.Max(remaining, 0), maxTimerDelay))
		next := time.AfterFunc(wait, func() {
			mu.Lock()
			if stopped {
				mu.Unlock()
				return
			}
			if remaining > maxTimerDelay {
				arm(remaining - maxTimerDelay)
				mu.Unlock()
				return
			}
			mu.Unlock()
			callback()
		})
		timer = next
	}
	mu.Lock()
	arm(delayMs)
	mu.Unlock()
	return func() {
		mu.Lock()
		defer mu.Unlock()
		stopped = true
		if timer != nil {
			timer.Stop()
		}
	}
}

// setTimeoutDuration is the delay setTimeout waits for milliseconds: a delay below 1 ms or NaN is 1 ms, and a
// fractional delay is truncated to whole milliseconds.
func setTimeoutDuration(milliseconds float64) time.Duration {
	if !(milliseconds >= 1) {
		milliseconds = 1
	}
	return time.Duration(math.Trunc(milliseconds)) * time.Millisecond //portlint:allow numbers milliseconds is at least 1 and at most 2^31-1 (the caller caps it), so the conversion is exact
}

// Timers are the timers the Session and Harness arm through the host's SetTimer (timers.ts Timers).
type Timers struct {
	// Foreground arms a timer that paces or resumes work; when the host panics on or declines it, a Go timer arms it
	// instead.
	Foreground func(callback func(), delayMs float64) (disarm func())
	// Background arms a timer that only frees memory; nil when the host declines it.
	Background func(callback func(), delayMs float64) (disarm func())
}

// HostTimers wraps a host SetTimer (timers.ts hostTimers). A foreground timer must fire, or work stalls: one the host
// panics on or declines is armed with NativeTimer, and the first such failure is reported. A background one the host
// panics on is reported and declined.
func HostTimers(setTimer SetTimer, report func(err error)) Timers {
	arm := setTimer
	if arm == nil {
		arm = NativeTimer
	}
	var mu sync.Mutex
	reported := false
	fallBack := func(err error) {
		mu.Lock()
		first := !reported
		reported = true
		mu.Unlock()
		if first {
			report(err)
		}
	}
	try := func(callback func(), delayMs float64, background bool) (disarm func(), failure error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				disarm, failure = nil, panicError(recovered)
			}
		}()
		return arm(callback, delayMs, background), nil
	}
	return Timers{
		Foreground: func(callback func(), delayMs float64) func() {
			disarm, failure := try(callback, delayMs, false)
			switch {
			case failure != nil:
				fallBack(failure)
			case disarm != nil:
				return disarm
			default:
				fallBack(errTimerDeclined)
			}
			return NativeTimer(callback, delayMs, false)
		},
		Background: func(callback func(), delayMs float64) func() {
			disarm, failure := try(callback, delayMs, true)
			if failure != nil {
				report(failure)
				return nil
			}
			return disarm
		},
	}
}

type timerError string

func (err timerError) Error() string { return string(err) }

var errTimerDeclined error = timerError("HarnessOptions.setTimer declined a timer that paces work; using setTimeout")

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return timerError(ErrorMessage(recovered))
}
