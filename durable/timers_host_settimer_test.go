package durable

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// timers.ts hostTimers: the host's SetTimer is handed the callback and delay it was asked for, and whether the timer only
// frees memory (Background) or paces work (Foreground); the disarm it returns is the one the caller gets, and the
// callback it was given runs when the host fires it.
func TestHostTimersHandTheHostSetTimerItsCallbackDelayAndKind(t *testing.T) {
	type armed struct {
		callback   func()
		delayMs    float64
		background bool
		disarmed   *atomic.Int32
	}
	var mu sync.Mutex
	var calls []armed
	var host SetTimer = func(callback func(), delayMs float64, background bool) func() {
		disarmed := new(atomic.Int32)
		mu.Lock()
		calls = append(calls, armed{callback, delayMs, background, disarmed})
		mu.Unlock()
		return func() { disarmed.Add(1) }
	}
	var reports []error
	timers := HostTimers(host, func(err error) { reports = append(reports, err) })

	var foregroundFired, backgroundFired atomic.Int32
	foreground := timers.Foreground(func() { foregroundFired.Add(1) }, 25)
	background := timers.Background(func() { backgroundFired.Add(1) }, 90)
	if foreground == nil || background == nil {
		t.Fatalf("disarms = %v, %v, want the host's", foreground != nil, background != nil)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("the host armed %d timers, want 2", len(calls))
	}
	if calls[0].delayMs != 25 || calls[0].background {
		t.Fatalf("foreground timer armed as delay %v background %v, want 25 and false", calls[0].delayMs, calls[0].background)
	}
	if calls[1].delayMs != 90 || !calls[1].background {
		t.Fatalf("background timer armed as delay %v background %v, want 90 and true", calls[1].delayMs, calls[1].background)
	}
	calls[0].callback()
	calls[1].callback()
	calls[1].callback()
	if foregroundFired.Load() != 1 || backgroundFired.Load() != 2 {
		t.Fatalf("callbacks ran %d and %d times, want the caller's own callbacks 1 and 2", foregroundFired.Load(), backgroundFired.Load())
	}
	foreground()
	if calls[0].disarmed.Load() != 1 || calls[1].disarmed.Load() != 0 {
		t.Fatalf("disarmed %d and %d, want only the foreground timer", calls[0].disarmed.Load(), calls[1].disarmed.Load())
	}
	background()
	if calls[1].disarmed.Load() != 1 {
		t.Fatalf("background disarmed %d times, want 1", calls[1].disarmed.Load())
	}
	if len(reports) != 0 {
		t.Fatalf("reports %v, want none for a host that armed both", reports)
	}
}

// Without a host SetTimer both kinds are native timers: a background timer is never declined there (NativeTimer).
func TestHostTimersWithoutAHostSetTimerArmNativeTimersForBothKinds(t *testing.T) {
	var host SetTimer
	timers := HostTimers(host, func(err error) { t.Errorf("reported %v", err) })
	fired := make(chan string, 2)
	disarmForeground := timers.Foreground(func() { fired <- "foreground" }, 1)
	disarmBackground := timers.Background(func() { fired <- "background" }, 1)
	if disarmForeground == nil || disarmBackground == nil {
		t.Fatalf("disarms %v and %v, want both armed", disarmForeground != nil, disarmBackground != nil)
	}
	got := map[string]bool{}
	for range 2 {
		select {
		case kind := <-fired:
			got[kind] = true
		case <-time.After(10 * time.Second):
			t.Fatalf("only %v fired", got)
		}
	}
	if !got["foreground"] || !got["background"] {
		t.Fatalf("fired %v, want both kinds", got)
	}
}
