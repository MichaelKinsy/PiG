// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

type timerRuntime = durable.TaskRuntime[durable.JsonValue, napState, durable.JsonValue, any]
type timerRecord = durable.RunningTask[durable.JsonValue, napState, durable.JsonValue]
type timerNext = durable.NextTaskState[napState, durable.JsonValue]

func completeTimerTask(ctx context.Context, runtime timerRuntime) error {
	return runtime.Commit(ctx, func(durable.Tx, timerRecord) (*timerNext, error) {
		return &timerNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: new(durable.JsonValue)}}, nil
	})
}

// A timer armed while its invocation ends is disarmed, so no host timer outlives the invocation (scheduler.ts setTimer and the
// end that disarms invocation.timers).
func TestATimerArmedAsItsInvocationEndsIsDisarmed(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		entered := make(chan struct{})
		release := make(chan struct{})
		registered := make(chan struct{})
		var mu sync.Mutex
		disarmed := false
		setTimer := func(callback func(), delayMs float64, background bool) func() {
			if background {
				return nil
			}
			if delayMs == 777 {
				close(entered)
				<-release
				return func() { mu.Lock(); disarmed = true; mu.Unlock() }
			}
			return durable.NativeTimer(callback, delayMs, background)
		}
		task := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, durable.JsonValue, any]{
			Name: "test.race", Version: 1,
			Initial: func(durable.JsonValue) napState { return napState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, durable.JsonValue, any]{
				"run": func(ctx context.Context, _ timerRecord, runtime timerRuntime) error {
					go func() { runtime.SetTimer(func() {}, 777); close(registered) }()
					<-entered
					return completeTimerTask(ctx, runtime)
				},
			},
			Abort: func(context.Context, timerRecord, timerRuntime) error { return nil },
		})
		rig := newSleepRig(t, core, task.Erased())
		h := rig.open(func(o *harness.HarnessOptions) { o.SetTimer = setTimer })
		id := rig.start(h, task.Erased())
		h.Resume()
		waitFinished(t, h, id)
		settleScheduling(t, h)
		close(release)
		<-registered
		// The terminal record is visible before the invocation's end runs on its own goroutine, and the end is what disarms.
		pollUntil(t, "the timer armed while the invocation ended to be disarmed", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return disarmed
		})
	})
}

// A timer that fired leaves the invocation's timers, so the end disarms only live ones and a long phase keeps no fired timer
// (scheduler.ts:1915-1922 deletes on fire).
func TestAFiredTimerIsNotKeptUntilTheInvocationEnds(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		timers := &fakeTimers{}
		fired := make(chan struct{})
		proceed := make(chan struct{})
		task := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, durable.JsonValue, any]{
			Name: "test.fired", Version: 1,
			Initial: func(durable.JsonValue) napState { return napState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, durable.JsonValue, any]{
				"run": func(ctx context.Context, _ timerRecord, runtime timerRuntime) error {
					for range 3 {
						runtime.SetTimer(func() { fired <- struct{}{} }, 555)
					}
					runtime.SetTimer(func() {}, 999)
					<-proceed
					return completeTimerTask(ctx, runtime)
				},
			},
			Abort: func(context.Context, timerRecord, timerRuntime) error { return nil },
		})
		rig := newSleepRig(t, core, task.Erased())
		h := rig.open(func(o *harness.HarnessOptions) { o.SetTimer = timers.setTimer })
		id := rig.start(h, task.Erased())
		h.Resume()
		pollUntil(t, "three timers", func() bool {
			n := 0
			for _, x := range timers.all() {
				if x.delayMs == 555 {
					n++
				}
			}
			return n == 3
		})
		for _, x := range timers.all() {
			if x.delayMs == 555 {
				go x.callback()
				<-fired
			}
		}
		close(proceed)
		waitFinished(t, h, id)
		settleScheduling(t, h)
		pollUntil(t, "control timer disarmed", func() bool {
			for _, x := range timers.all() {
				if x.delayMs == 999 {
					return timers.isDisarmed(x)
				}
			}
			return false
		})
		n := 0
		for _, x := range timers.all() {
			if x.delayMs == 555 && timers.isDisarmed(x) {
				n++
			}
		}
		if n != 0 {
			t.Errorf("%d fired timers were disarmed at the invocation's end (Pi deletes a fired timer from invocation.timers)", n)
		}
	})
}

// Wake on a closed Session fails as Pi's assertUsable does (harness.ts:267, session.ts:870-872).
func TestWakeAfterCloseReportsTheSessionClosed(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		rig := newSleepRig(t, core)
		h := rig.open(nil)
		if err := h.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		var got any
		func() { defer func() { got = recover() }(); h.Wake() }()
		err, _ := got.(error)
		if err == nil || err.Error() != "Session is closed" {
			t.Errorf("Wake after close panicked with %v, want Session is closed", got)
		}
	})
}

// A sleep until a negative time names it as JavaScript prints the number (${until}, scheduler.ts:2070).
func TestASleepUntilANegativeTimeNamesItAsJavaScriptPrintsIt(t *testing.T) {
	eachEngine(t, func(t *testing.T, core bool) {
		var mu sync.Mutex
		var errs []string
		task := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, durable.JsonValue, any]{
			Name: "test.msgs", Version: 1,
			Initial: func(durable.JsonValue) napState { return napState{Phase: "run"} },
			Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, durable.JsonValue, any]{
				"run": func(ctx context.Context, _ timerRecord, runtime timerRuntime) error {
					for _, until := range []float64{-1e21, -1e-7, -0.5} {
						if err := runtime.SleepUntil(ctx, until, napState{Phase: "run"}); err != nil {
							mu.Lock()
							errs = append(errs, err.Error())
							mu.Unlock()
						}
					}
					return completeTimerTask(ctx, runtime)
				},
			},
			Abort: func(context.Context, timerRecord, timerRuntime) error { return nil },
		})
		rig := newSleepRig(t, core, task.Erased())
		h := rig.open(nil)
		id := rig.start(h, task.Erased())
		h.Resume()
		waitFinished(t, h, id)
		mu.Lock()
		defer mu.Unlock()
		var want []string
		for _, shown := range []string{"-1e+21", "-1e-7", "-0.5"} {
			want = append(want, fmt.Sprintf("Task %d cannot sleep until %s: not a time in milliseconds", id, shown))
		}
		if strings.Join(errs, "\n") != strings.Join(want, "\n") {
			t.Errorf("got %q, want %q", errs, want)
		}
	})
}
