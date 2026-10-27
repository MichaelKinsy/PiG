package durable

// pi: packages/durable/src/timers.ts

// Ports packages/durable/src/timers.ts: the default setTimer and the wrapper that falls back to it.

import (
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"
)

// A timer fires once after its delay and a disarmed one never fires (timers.ts nativeTimer).
func TestNativeTimerFiresOnceAndDisarms(t *testing.T) {
	fired := make(chan struct{}, 2)
	NativeTimer(func() { fired <- struct{}{} }, 1, false)
	select {
	case <-fired:
	case <-time.After(10 * time.Second):
		t.Fatal("the timer never fired")
	}
	var late atomic.Int32
	disarm := NativeTimer(func() { late.Add(1) }, 1, true)
	disarm()
	// A later timer fires after the disarmed one's time has passed.
	NativeTimer(func() { fired <- struct{}{} }, 30, false)
	select {
	case <-fired:
	case <-time.After(10 * time.Second):
		t.Fatal("the later timer never fired")
	}
	if late.Load() != 0 {
		t.Fatalf("a disarmed timer fired %d times", late.Load())
	}
}

// A disarmed timer keeps nothing of its callback (timers.ts nativeTimer: clearTimeout leaves nothing of it). The runtime keeps a stopped
// Go timer, and the function it was armed with, in its timer heap until it next removes it, so a callback that reaches a Session
// would keep the Session alive after the Session disarmed the timer at its close. Each round arms and disarms one timer, as a
// Session does its document sweep, and collects once: the callback's object must be gone at that collection.
func TestNativeTimerDisarmReleasesItsCallback(t *testing.T) {
	const rounds = 8
	for round := range rounds {
		held := armAndDisarmReachingTimer()
		runtime.GC()
		if held.Value() != nil {
			t.Fatalf("round %d: a disarmed timer keeps its callback alive", round)
		}
	}
}

// armAndDisarmReachingTimer arms a timer whose callback reaches an object, disarms it, and returns a weak pointer to the object.
func armAndDisarmReachingTimer() weak.Pointer[[64]byte] {
	object := new([64]byte)
	held := weak.Make(object)
	// A delay far past the test: the timer is disarmed long before it could fire.
	disarm := NativeTimer(func() { object[0]++ }, 3_600_000, true)
	disarm()
	return held
}

// A foreground timer the host throws on or declines is armed by the native timer and the failure is reported once; a background one the
// host throws on is reported and declined (timers.ts hostTimers).
func TestHostTimersFallBackForegroundAndDeclineBackground(t *testing.T) {
	var reports []string
	report := func(err error) { reports = append(reports, err.Error()) }
	declining := HostTimers(func(func(), float64, bool) func() { return nil }, report)
	fired := make(chan struct{}, 4)
	declining.Foreground(func() { fired <- struct{}{} }, 1)
	declining.Foreground(func() { fired <- struct{}{} }, 1)
	for range 2 {
		select {
		case <-fired:
		case <-time.After(10 * time.Second):
			t.Fatal("a declined foreground timer never fired")
		}
	}
	if len(reports) != 1 || reports[0] != "HarnessOptions.setTimer declined a timer that paces work; using setTimeout" {
		t.Fatalf("reports %q, want the decline once", reports)
	}
	if disarm := declining.Background(func() {}, 1); disarm != nil {
		t.Fatal("a background timer the host declined was armed")
	}

	reports = nil
	throwing := HostTimers(func(func(), float64, bool) func() { panic(errors.New("no timers")) }, report)
	throwing.Foreground(func() { fired <- struct{}{} }, 1)
	select {
	case <-fired:
	case <-time.After(10 * time.Second):
		t.Fatal("a foreground timer the host threw on never fired")
	}
	if throwing.Background(func() {}, 1) != nil {
		t.Fatal("a background timer the host threw on was armed")
	}
	if len(reports) != 2 || reports[0] != "no timers" || reports[1] != "no timers" {
		t.Fatalf("reports %q, want the throw reported for each timer kind", reports)
	}
}
