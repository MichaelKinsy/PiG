package durable

// pi: packages/durable/src/timers.ts

// Ports packages/durable/src/timers.ts: the default setTimer and the wrapper that falls back to it.

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
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
