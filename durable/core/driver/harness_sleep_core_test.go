// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/core/driver"
	"github.com/MichaelKinsy/PiG/durable/harness"
	sqlitenode "github.com/MichaelKinsy/PiG/durable/storage/sqlite/node"
)

// Ports packages/durable/test/harness-sleep.test.ts (Pi f75b3240ab) over both engines: every case runs on the reference Harness
// and on the core, with Pi's expectations. Time is the Harness clock the tests move (HarnessOptions.Now) and the timers are
// the fakes they pass as HarnessOptions.SetTimer, so nothing here waits for a real deadline.

type napState struct {
	Phase string  `json:"phase"`
	Slept float64 `json:"slept,omitempty"`
}

type (
	napRuntime = durable.TaskRuntime[durable.JsonValue, napState, float64, any]
	napRecord  = durable.RunningTask[durable.JsonValue, napState, float64]
	napNext    = durable.NextTaskState[napState, float64]
)

// napTask sleeps delay ms on the Harness clock, then completes with the time it resumed at (harness-sleep.test.ts:30).
func napTask(delay float64, runs *atomic.Int32) durable.Task[durable.JsonValue, napState, float64, any] {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, float64, any]{
		Name: "test.nap", Version: 1,
		Initial: func(durable.JsonValue) napState { return napState{Phase: "start"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, float64, any]{
			"start": func(ctx context.Context, _ napRecord, runtime napRuntime) error {
				return runtime.SleepUntil(ctx, runtime.Now()+delay, napState{Phase: "after", Slept: delay})
			},
			"after": func(ctx context.Context, _ napRecord, runtime napRuntime) error {
				if runs != nil {
					runs.Add(1)
				}
				resumed := runtime.Now()
				return runtime.Commit(ctx, func(durable.Tx, napRecord) (*napNext, error) {
					return &napNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[float64]{Status: durable.OutcomeCompleted, Result: &resumed}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ napRecord, runtime napRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, napRecord) (*napNext, error) {
				reason := "test"
				return &napNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[float64]{Status: durable.OutcomeAborted, Reason: &reason}}, nil
			})
		},
	})
}

type fakeTimer struct {
	delayMs  float64
	callback func()
	disarmed bool
}

// fakeTimers is a host SetTimer that records the timers that pace work and declines background ones (harness-sleep.test.ts:75).
type fakeTimers struct {
	mu    sync.Mutex
	armed []*fakeTimer
}

func (timers *fakeTimers) setTimer(callback func(), delayMs float64, background bool) func() {
	if background {
		return nil
	}
	timer := &fakeTimer{delayMs: delayMs, callback: callback}
	timers.mu.Lock()
	timers.armed = append(timers.armed, timer)
	timers.mu.Unlock()
	return func() {
		timers.mu.Lock()
		timer.disarmed = true
		timers.mu.Unlock()
	}
}

func (timers *fakeTimers) live() []*fakeTimer {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	var live []*fakeTimer
	for _, timer := range timers.armed {
		if !timer.disarmed {
			live = append(live, timer)
		}
	}
	return live
}

func (timers *fakeTimers) liveWith(delayMs float64) *fakeTimer {
	for _, timer := range timers.live() {
		if timer.delayMs == delayMs {
			return timer
		}
	}
	return nil
}

func (timers *fakeTimers) all() []*fakeTimer {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	return append([]*fakeTimer(nil), timers.armed...)
}

func (timers *fakeTimers) isDisarmed(timer *fakeTimer) bool {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	return timer.disarmed
}

type clockValue struct{ value atomic.Int64 }

func newClock(at int64) *clockValue { c := &clockValue{}; c.value.Store(at); return c }

func (c *clockValue) now() float64 { return float64(c.value.Load()) }

func (c *clockValue) set(at int64) { c.value.Store(at) }

type wakeLog struct {
	mu    sync.Mutex
	wakes []harness.Wake
}

func (log *wakeLog) add(wake harness.Wake) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.wakes = append(log.wakes, wake)
}

func (log *wakeLog) all() []harness.Wake {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]harness.Wake(nil), log.wakes...)
}

func (log *wakeLog) last() (harness.Wake, bool) {
	all := log.all()
	if len(all) == 0 {
		return harness.Wake{}, false
	}
	return all[len(all)-1], true
}

func wakeIs(wake harness.Wake, active bool, at ...float64) bool {
	if wake.Active != active {
		return false
	}
	if len(at) == 0 {
		return wake.At == nil
	}
	return wake.At != nil && *wake.At == at[0]
}

func showWake(wake harness.Wake) string {
	text := "{active: " + strconv.FormatBool(wake.Active)
	if wake.At != nil {
		text += ", at: " + strconv.FormatFloat(*wake.At, 'f', -1, 64)
	}
	return text + "}"
}

func expectWake(t *testing.T, got harness.Wake, active bool, at ...float64) {
	t.Helper()
	if !wakeIs(got, active, at...) {
		t.Fatalf("wake %s, want active %v at %v", showWake(got), active, at)
	}
}

type reportList struct {
	mu      sync.Mutex
	reports []error
}

func (list *reportList) add(err error) {
	list.mu.Lock()
	defer list.mu.Unlock()
	list.reports = append(list.reports, err)
}

func (list *reportList) all() []error {
	list.mu.Lock()
	defer list.mu.Unlock()
	return append([]error(nil), list.reports...)
}

// sleepRig opens one engine's Harness over a SQLite file, again and again for a reopen.
type sleepRig struct {
	t        *testing.T
	core     bool
	path     string
	registry harness.Registry
	reports  *reportList
}

func newSleepRig(t *testing.T, core bool, tasks ...durable.AnyTask) *sleepRig {
	t.Helper()
	registry := harness.CreateRegistry()
	if len(tasks) > 0 {
		if err := registry.Install(&durable.Extension{Name: "tasks", Tasks: tasks}); err != nil {
			t.Fatal(err)
		}
	}
	return &sleepRig{t: t, core: core, path: filepath.Join(t.TempDir(), "session.sqlite"), registry: registry, reports: &reportList{}}
}

func (rig *sleepRig) open(configure func(*harness.HarnessOptions)) harness.Harness {
	rig.t.Helper()
	options := harness.HarnessOptions{Models: ai.CreateModels(), Registry: rig.registry, OnReport: func(err error, _ durable.ReportInfo) { rig.reports.add(err) }}
	if configure != nil {
		configure(&options)
	}
	var opened harness.Harness
	var err error
	if rig.core {
		opened, err = driver.OpenHarness(rig.t.Context(), rig.path, options)
	} else {
		var store durable.Storage
		if store, err = sqlitenode.OpenNodeSqliteStorage(rig.path, sqlitenode.NodeSqliteStorageOptions{}); err != nil {
			rig.t.Fatal(err)
		}
		opened, err = harness.OpenHarness(rig.t.Context(), store, options)
	}
	if err != nil {
		rig.t.Fatal(err)
	}
	rig.t.Cleanup(func() { _ = opened.Close(context.Background()) })
	return opened
}

func (rig *sleepRig) start(h harness.Harness, task durable.AnyTask) durable.TaskId {
	rig.t.Helper()
	root, err := h.Root(rig.t.Context(), nil)
	if err != nil {
		rig.t.Fatal(err)
	}
	return startTask(rig.t, root, task)
}

func stateOf(t *testing.T, h harness.Harness, id durable.TaskId) durable.TaskState[durable.JsonValue, durable.JsonValue] {
	t.Helper()
	record, err := h.GetTask(t.Context(), id)
	if err != nil || record == nil {
		t.Fatalf("GetTask(%d) = %v, %v", id, record, err)
	}
	return record.State
}

func isStatus(t *testing.T, h harness.Harness, id durable.TaskId, status durable.TaskStatus) func() bool {
	return func() bool {
		record, err := h.GetTask(t.Context(), id)
		return err == nil && record != nil && record.State.Status == status
	}
}

// settleScheduling returns once every scheduling pass queued so far has run: an Inspect is a line read, and a pass takes its place
// on the line when it is started, so the read comes after it.
func settleScheduling(t *testing.T, h harness.Harness) {
	t.Helper()
	for range 2 {
		if _, err := h.Inspect(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func completedResult(t *testing.T, receipt durable.SettledTask[durable.JsonValue]) float64 {
	t.Helper()
	outcome := receipt.State.Outcome
	if outcome == nil || outcome.Status != durable.OutcomeCompleted || outcome.Result == nil {
		t.Fatalf("outcome %+v, want completed", outcome)
	}
	result, ok := (*outcome.Result).(float64)
	if !ok {
		t.Fatalf("result %v is not a number", *outcome.Result)
	}
	return result
}

func waitFinished(t *testing.T, h harness.Harness, id durable.TaskId) durable.SettledTask[durable.JsonValue] {
	t.Helper()
	receipt, err := h.WaitForTask(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func jsonValueOf(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestSleepingTasks(t *testing.T) {
	// harness-sleep.test.ts:88 "sleeps without an invocation and resumes at its checkpoint once due"
	t.Run("sleeps without an invocation and resumes at its checkpoint once due", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			wakes := &wakeLog{}
			var runs atomic.Int32
			nap := napTask(1000, &runs)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.OnWake = clock.now, wakes.add })
			graph, err := h.TaskGraph(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the task sleeping", isStatus(t, h, id, durable.TaskSleeping))
			want := map[string]any{"status": "sleeping", "checkpoint": map[string]any{"phase": "after", "slept": float64(1000)}, "until": float64(2000)}
			if got := jsonValueOf(t, stateOf(t, h, id)); !reflect.DeepEqual(got, want) {
				t.Fatalf("state %v, want %v", got, want)
			}
			// The invocation ends just after its sleeping commit publishes, so the inspection settles on sleeping.
			inspected := func() (harness.TaskInspectionState, bool) {
				inspection, err := h.Inspect(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				for _, task := range inspection.Tasks {
					if task.Record.Id == id {
						return task.State, true
					}
				}
				return harness.TaskInspectionState{}, false
			}
			pollUntil(t, "the sleeping inspection", func() bool {
				state, ok := inspected()
				return ok && state.Kind == harness.TaskInspectionSleeping && state.Until == 2000
			})
			pollUntil(t, "the graph showing the sleep", func() bool {
				node, ok := graph.Value().Tasks[strconv.FormatInt(int64(id), 10)]
				return ok && node.State.Status == durable.TaskSleeping && node.State.Phase == "after" && node.State.Until == 2000
			})
			// harness-sleep.test.ts:109: the wake is idle once open's recovery pass has landed.
			pollUntil(t, "open's recovery pass landed", func() bool { return !h.Wake().Active })
			expectWake(t, h.Wake(), false, 2000)
			pollUntil(t, "the wake offered", func() bool { last, ok := wakes.last(); return ok && wakeIs(last, false, 2000) })

			// Early and repeated wakes run nothing.
			clock.set(1999)
			h.Resume()
			h.Resume()
			settleScheduling(t, h)
			if runs.Load() != 0 || stateOf(t, h, id).Status != durable.TaskSleeping {
				t.Fatalf("an early wake ran the task: runs %d, state %s", runs.Load(), stateOf(t, h, id).Status)
			}

			clock.set(2000)
			h.Resume()
			h.Resume()
			if got := completedResult(t, waitFinished(t, h, id)); got != 2000 {
				t.Fatalf("resumed at %v, want 2000", got)
			}
			if runs.Load() != 1 {
				t.Fatalf("after phase ran %d times, want 1", runs.Load())
			}
			pollUntil(t, "nothing left to do", func() bool { last, ok := wakes.last(); return ok && wakeIs(last, false) })
			// harness-sleep.test.ts:129
			pollUntil(t, "the wake idle", func() bool { return !h.Wake().Active })
			expectWake(t, h.Wake(), false)
			// Only changes: idle at open, running, asleep until 2000, running again, idle.
			all := wakes.all()
			for index := 1; index < len(all); index++ {
				if wakeIs(all[index], all[index-1].Active, func() []float64 {
					if all[index-1].At == nil {
						return nil
					}
					return []float64{*all[index-1].At}
				}()...) {
					t.Fatalf("wake %d repeats its predecessor: %s", index, showWake(all[index]))
				}
			}
			// harness-sleep.test.ts:133: Pi dropped the assertion that the first wake is idle: open's recovery pass keeps the
			// Session active until it lands, so the first offered wake may be active.
			var sawActive, sawSleep bool
			for _, wake := range all {
				sawActive = sawActive || wakeIs(wake, true)
				sawSleep = sawSleep || wakeIs(wake, false, 2000)
			}
			if !sawActive || !sawSleep {
				t.Fatalf("wakes %v lack an active one (%v) or the sleep (%v)", all, sawActive, sawSleep)
			}
			graph.Dispose()
		})
	})

	// harness-sleep.test.ts:140 "keeps a sleep across reopen"
	t.Run("keeps a sleep across reopen", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			nap := napTask(60_000, nil)
			rig := newSleepRig(t, core, nap.Erased())
			configure := func(o *harness.HarnessOptions) { o.Now, o.OnWake = clock.now, func(harness.Wake) {} }
			h := rig.open(configure)
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the task sleeping", isStatus(t, h, id, durable.TaskSleeping))
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}

			h = rig.open(configure)
			// Known from storage before scheduling starts, once open's recovery pass is done (harness-sleep.test.ts:152).
			pollUntil(t, "open's recovery pass landed", func() bool { return !h.Wake().Active })
			expectWake(t, h.Wake(), false, 61_000)
			h.Resume()
			settleScheduling(t, h)
			if stateOf(t, h, id).Status != durable.TaskSleeping {
				t.Fatalf("the reopened sleep is %s, want sleeping", stateOf(t, h, id).Status)
			}
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}

			clock.set(61_000)
			h = rig.open(configure)
			expectWake(t, h.Wake(), true)
			h.Resume()
			if got := completedResult(t, waitFinished(t, h, id)); got != 61_000 {
				t.Fatalf("resumed at %v, want 61000", got)
			}
		})
	})

	// harness-sleep.test.ts:167 "resumes a due sleep with its timer without onWake, with background timers declined"
	t.Run("resumes a due sleep with its timer without onWake, with background timers declined", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			timers := &fakeTimers{}
			nap := napTask(500, nil)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.SetTimer = clock.now, timers.setTimer })
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the sleep's timer", func() bool { return timers.liveWith(500) != nil })
			if stateOf(t, h, id).Status != durable.TaskSleeping {
				t.Fatalf("the task is %s, want sleeping", stateOf(t, h, id).Status)
			}
			clock.set(1500)
			timers.liveWith(500).callback()
			// Polled: WaitForTask would call Resume, which wakes due sleeps itself.
			pollUntil(t, "the task terminal", isStatus(t, h, id, durable.TaskTerminal))
			outcome := stateOf(t, h, id).Outcome
			if outcome == nil || outcome.Status != durable.OutcomeCompleted || (*outcome.Result).(float64) != 1500 {
				t.Fatalf("outcome %+v, want completed at 1500", outcome)
			}
		})
	})

	// harness-sleep.test.ts:187 "runs a due sleep at any scheduling pass, before its timer fires"
	t.Run("runs a due sleep at any scheduling pass, before its timer fires", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			timers := &fakeTimers{}
			nap := napTask(500, nil)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.SetTimer = clock.now, timers.setTimer })
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the task sleeping", isStatus(t, h, id, durable.TaskSleeping))
			// The deadline passes, and an unrelated change starts a pass before the timer fires.
			clock.set(1500)
			if err := rig.registry.Install(&durable.Extension{Name: "unrelated"}); err != nil {
				t.Fatal(err)
			}
			pollUntil(t, "the task terminal", isStatus(t, h, id, durable.TaskTerminal))
			outcome := stateOf(t, h, id).Outcome
			if outcome == nil || outcome.Status != durable.OutcomeCompleted || (*outcome.Result).(float64) != 1500 {
				t.Fatalf("outcome %+v, want completed at 1500", outcome)
			}
		})
	})

	// harness-sleep.test.ts:207 "disarms the sleep timer when the sleep is aborted and when the Harness closes"
	t.Run("disarms the sleep timer when the sleep is aborted and when the Harness closes", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			timers := &fakeTimers{}
			short := napTask(500, nil)
			rig := newSleepRig(t, core, short.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.SetTimer = clock.now, timers.setTimer })
			aborted := rig.start(h, short.Erased())
			h.Resume()
			pollUntil(t, "the sleep's timer", func() bool { return timers.liveWith(500) != nil })
			if _, err := h.AbortTask(t.Context(), aborted); err != nil {
				t.Fatal(err)
			}
			waitFinished(t, h, aborted)
			pollUntil(t, "every timer disarmed", func() bool { return len(timers.live()) == 0 })
			kept := rig.start(h, short.Erased())
			pollUntil(t, "the second task sleeping", isStatus(t, h, kept, durable.TaskSleeping))
			pollUntil(t, "its timer", func() bool { return len(timers.live()) == 1 })
			if live := timers.live(); live[0].delayMs != 500 {
				t.Fatalf("live timer delays %v, want [500]", live[0].delayMs)
			}
			if err := h.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if live := timers.live(); len(live) != 0 {
				t.Fatalf("%d timers armed after close", len(live))
			}
		})
	})

	// harness-sleep.test.ts:228 "disarms an invocation's timers when it ends, and a late fire runs nothing"
	t.Run("disarms an invocation's timers when it ends, and a late fire runs nothing", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var fired atomic.Int32
			type timedRuntime = durable.TaskRuntime[durable.JsonValue, napState, durable.JsonValue, any]
			type timedRecord = durable.RunningTask[durable.JsonValue, napState, durable.JsonValue]
			timed := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, durable.JsonValue, any]{
				Name: "test.timed", Version: 1,
				Initial: func(durable.JsonValue) napState { return napState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, durable.JsonValue, any]{
					"run": func(ctx context.Context, _ timedRecord, runtime timedRuntime) error {
						runtime.SetTimer(func() { fired.Add(1) }, 60_000)
						return runtime.Commit(ctx, func(durable.Tx, timedRecord) (*durable.NextTaskState[napState, durable.JsonValue], error) {
							return &durable.NextTaskState[napState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: new(durable.JsonValue)}}, nil
						})
					},
				},
				Abort: func(context.Context, timedRecord, timedRuntime) error { return nil },
			})
			timers := &fakeTimers{}
			rig := newSleepRig(t, core, timed.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.SetTimer = timers.setTimer })
			id := rig.start(h, timed.Erased())
			h.Resume()
			waitFinished(t, h, id)
			var timer *fakeTimer
			for _, candidate := range timers.all() {
				if candidate.delayMs == 60_000 {
					timer = candidate
				}
			}
			if timer == nil {
				t.Fatal("the phase's timer was never armed")
			}
			pollUntil(t, "the timer disarmed", func() bool { return timers.isDisarmed(timer) })
			// A host timer that fires anyway: the ended invocation's callback does not run.
			timer.callback()
			if fired.Load() != 0 {
				t.Fatalf("an ended invocation's timer fired %d times", fired.Load())
			}
		})
	})

	// harness-sleep.test.ts:259 "arms no timer for sleeps with onWake"
	t.Run("arms no timer for sleeps with onWake", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			timers := &fakeTimers{}
			wakes := &wakeLog{}
			nap := napTask(500, nil)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.SetTimer, o.OnWake = clock.now, timers.setTimer, wakes.add })
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the task sleeping", isStatus(t, h, id, durable.TaskSleeping))
			// The wake is offered after anything that would arm a timer.
			pollUntil(t, "the sleep offered", func() bool { last, ok := wakes.last(); return ok && wakeIs(last, false, 1500) })
			if armed := timers.all(); len(armed) != 0 {
				t.Fatalf("%d timers armed for a sleep with onWake", len(armed))
			}
			clock.set(1500)
			expectWake(t, h.Wake(), true)
			h.Resume()
			if outcome := waitFinished(t, h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
		})
	})

	// harness-sleep.test.ts:280 "arms a foreground timer the host declines with setTimeout, reporting it once"
	t.Run("arms a foreground timer the host declines with setTimeout, reporting it once", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			// The scheduler asks the host for a timer only for a sleep whose time is after the clock when the wake is computed (scheduler.ts wake: until > now), and a
			// sleep that is already due runs with no timer at all. A 10 ms nap on the system clock is due by then on a host that is 10 ms slow, and the decline is never
			// reported (CI: "reports []"). So the clock stands still until the host is asked for a timer and then passes that timer's delay in the call: the sleeps can end
			// only through the timers the host declined, which the fallback arms, and the decline is reported the one time.
			clock := newClock(1000)
			var asked atomic.Int32
			nap := napTask(10, nil)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) {
				o.Now = clock.now
				o.SetTimer = func(_ func(), delayMs float64, _ bool) func() {
					asked.Add(1)
					clock.set(int64(clock.now() + delayMs))
					return nil
				}
			})
			first := rig.start(h, nap.Erased())
			second := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the host asked for a timer", func() bool { return asked.Load() > 0 })
			for _, id := range []durable.TaskId{first, second} {
				if outcome := waitFinished(t, h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
					t.Fatalf("task %d outcome %+v, want completed", id, outcome)
				}
			}
			reports := rig.reports.all()
			if len(reports) != 1 || reports[0].Error() != "HarnessOptions.setTimer declined a timer that paces work; using setTimeout" {
				t.Fatalf("reports %v, want the decline once", reports)
			}
		})
	})

	// harness-sleep.test.ts:294 "counts neither a wait on a sleeping task nor an outcome held over one as active"
	t.Run("counts neither a wait on a sleeping task nor an outcome held over one as active", func(t *testing.T) {
		for _, hold := range []bool{false, true} {
			eachEngine(t, func(t *testing.T, core bool) {
				clock := newClock(1000)
				child := napTask(500, nil)
				type parentState struct {
					Phase string          `json:"phase"`
					Child *durable.TaskId `json:"child,omitempty"`
				}
				type parentInput struct {
					Hold bool `json:"hold"`
				}
				type parentRuntime = durable.TaskRuntime[parentInput, parentState, durable.JsonValue, any]
				type parentRecord = durable.RunningTask[parentInput, parentState, durable.JsonValue]
				type parentNext = durable.NextTaskState[parentState, durable.JsonValue]
				parent := durable.DefineTask(durable.TaskDefinition[parentInput, parentState, durable.JsonValue, any]{
					Name: "test.parent", Version: 1,
					Initial: func(parentInput) parentState { return parentState{Phase: "start"} },
					Phases: map[string]durable.PhaseHandler[parentInput, parentState, durable.JsonValue, any]{
						"start": func(ctx context.Context, task parentRecord, runtime parentRuntime) error {
							return runtime.Commit(ctx, func(tx durable.Tx, _ parentRecord) (*parentNext, error) {
								id, err := tx.CreateTaskErased(child.Erased(), nil, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: task.Id}})
								if err != nil {
									return nil, err
								}
								checkpoint := parentState{Phase: "joined", Child: &id}
								// Held: completes next, holding its outcome over the sleeping child. Otherwise waits for it.
								if task.Input.Hold {
									return &parentNext{Status: durable.TaskRunning, Checkpoint: &checkpoint}, nil
								}
								return &parentNext{Status: durable.TaskWaiting, Checkpoint: &checkpoint, On: []durable.TaskId{id}, Policy: durable.JoinAllSettled}, nil
							})
						},
						"joined": func(ctx context.Context, _ parentRecord, runtime parentRuntime) error {
							return runtime.Commit(ctx, func(durable.Tx, parentRecord) (*parentNext, error) {
								return &parentNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: new(durable.JsonValue)}}, nil
							})
						},
					},
					Abort: func(context.Context, parentRecord, parentRuntime) error { return nil },
				})
				rig := newSleepRig(t, core, parent.Erased(), child.Erased())
				h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.OnWake = clock.now, func(harness.Wake) {} })
				root, err := h.Root(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				id, err := durable.Commit(t.Context(), root, func(tx durable.Tx) (durable.TaskId, error) {
					return durable.CreateTask(tx, parent, parentInput{Hold: hold}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
				})
				if err != nil {
					t.Fatal(err)
				}
				h.Resume()
				parked := durable.TaskWaiting
				if hold {
					parked = durable.TaskCompleting
				}
				pollUntil(t, "the parent "+string(parked), isStatus(t, h, id, parked))
				pollUntil(t, "the wake at the child's sleep", func() bool { return wakeIs(h.Wake(), false, 1500) })
				// harness-sleep.test.ts:333
				pollUntil(t, "the wake idle", func() bool { return !h.Wake().Active })
				expectWake(t, h.Wake(), false, 1500)
			})
		}
	})

	// harness-sleep.test.ts:337 "runs the abort handler of a sleeping task without waiting for its time"
	t.Run("runs the abort handler of a sleeping task without waiting for its time", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			nap := napTask(60_000, nil)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) { o.Now, o.OnWake = clock.now, func(harness.Wake) {} })
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the task sleeping", isStatus(t, h, id, durable.TaskSleeping))
			if _, err := h.AbortTask(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			outcome := waitFinished(t, h, id).State.Outcome
			if outcome == nil || outcome.Status != durable.OutcomeAborted || outcome.Reason == nil || *outcome.Reason != "test" {
				t.Fatalf("outcome %+v, want aborted (test)", outcome)
			}
		})
	})

	// harness-sleep.test.ts:349 "rejects a sleep until something that is not a time, and a sleep in an abort handler"
	t.Run("rejects a sleep until something that is not a time, and a sleep in an abort handler", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			var mu sync.Mutex
			var errs []string
			note := func(err error) {
				mu.Lock()
				defer mu.Unlock()
				errs = append(errs, err.Error())
			}
			completeNext := func() *napNext {
				return &napNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[float64]{Status: durable.OutcomeCompleted, Result: new(float64)}}
			}
			invalid := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, float64, any]{
				Name: "test.invalid-sleep", Version: 1,
				Initial: func(durable.JsonValue) napState { return napState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, float64, any]{
					"run": func(ctx context.Context, _ napRecord, runtime napRuntime) error {
						for _, until := range []float64{math.NaN(), -1, math.Inf(1)} {
							if err := runtime.SleepUntil(ctx, until, napState{Phase: "next"}); err != nil {
								note(err)
							}
						}
						return runtime.Commit(ctx, func(durable.Tx, napRecord) (*napNext, error) { return completeNext(), nil })
					},
					"next": func(context.Context, napRecord, napRuntime) error { return nil },
				},
				Abort: func(context.Context, napRecord, napRuntime) error { return nil },
			})
			sleepsInAbort := durable.DefineTask(durable.TaskDefinition[durable.JsonValue, napState, float64, any]{
				Name: "test.sleep-in-abort", Version: 1,
				Initial: func(durable.JsonValue) napState { return napState{Phase: "run"} },
				Phases: map[string]durable.PhaseHandler[durable.JsonValue, napState, float64, any]{
					"run": func(_ context.Context, _ napRecord, runtime napRuntime) error {
						<-runtime.Signal().Done()
						return context.Cause(runtime.Signal())
					},
				},
				Abort: func(ctx context.Context, _ napRecord, runtime napRuntime) error {
					if err := runtime.SleepUntil(ctx, runtime.Now()+10, napState{Phase: "run"}); err != nil {
						note(err)
					}
					return runtime.Commit(ctx, func(durable.Tx, napRecord) (*napNext, error) {
						reason := "test"
						return &napNext{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[float64]{Status: durable.OutcomeAborted, Reason: &reason}}, nil
					})
				},
			})
			rig := newSleepRig(t, core, invalid.Erased(), sleepsInAbort.Erased())
			h := rig.open(nil)
			id := rig.start(h, invalid.Erased())
			aborting := rig.start(h, sleepsInAbort.Erased())
			h.Resume()
			if outcome := waitFinished(t, h, id).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
			pollUntil(t, "the aborting task running", func() bool {
				inspection, err := h.Inspect(t.Context())
				if err != nil {
					return false
				}
				for _, task := range inspection.Tasks {
					if task.Record.Id == aborting && task.State.Kind == harness.TaskInspectionRunning {
						return true
					}
				}
				return false
			})
			if _, err := h.AbortTask(t.Context(), aborting); err != nil {
				t.Fatal(err)
			}
			if outcome := waitFinished(t, h, aborting).State.Outcome; outcome == nil || outcome.Status != durable.OutcomeAborted {
				t.Fatalf("outcome %+v, want aborted", outcome)
			}
			want := []string{
				"Task " + strconv.FormatInt(int64(id), 10) + " cannot sleep until NaN: not a time in milliseconds",
				"Task " + strconv.FormatInt(int64(id), 10) + " cannot sleep until -1: not a time in milliseconds",
				"Task " + strconv.FormatInt(int64(id), 10) + " cannot sleep until Infinity: not a time in milliseconds",
				"Abort handler of task " + strconv.FormatInt(int64(aborting), 10) + " cannot sleep",
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(errs, want) {
				t.Fatalf("errors %q, want %q", errs, want)
			}
		})
	})

	// harness-sleep.test.ts:403 "does not count a task without a definition as active"
	t.Run("does not count a task without a definition as active", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			missing := napTask(10, nil)
			rig := newSleepRig(t, core)
			h := rig.open(nil)
			// Created through a token the registry does not hold: nothing can run it.
			rig.start(h, missing.Erased())
			// harness-sleep.test.ts:411, :414
			pollUntil(t, "the wake idle", func() bool { return !h.Wake().Active })
			expectWake(t, h.Wake(), false)
			h.Resume()
			settleScheduling(t, h)
			pollUntil(t, "the wake idle", func() bool { return !h.Wake().Active })
			expectWake(t, h.Wake(), false)
		})
	})

	// harness-sleep.test.ts:416 "reports a throwing onWake and offers the same wake again at the next pass"
	t.Run("reports a throwing onWake and offers the same wake again at the next pass", func(t *testing.T) {
		eachEngine(t, func(t *testing.T, core bool) {
			clock := newClock(1000)
			wakes := &wakeLog{}
			failure := errors.New("alarm failed")
			var failing atomic.Bool
			failing.Store(true)
			nap := napTask(1000, nil)
			rig := newSleepRig(t, core, nap.Erased())
			h := rig.open(func(o *harness.HarnessOptions) {
				o.Now = clock.now
				o.OnWake = func(wake harness.Wake) {
					wakes.add(wake)
					if failing.Load() && wake.At != nil {
						panic(failure)
					}
				}
			})
			id := rig.start(h, nap.Erased())
			h.Resume()
			pollUntil(t, "the sleep offered", func() bool {
				for _, wake := range wakes.all() {
					if wakeIs(wake, false, 2000) {
						return true
					}
				}
				return false
			})
			pollUntil(t, "the failure reported", func() bool {
				for _, report := range rig.reports.all() {
					if report != nil && report.Error() == failure.Error() {
						return true
					}
				}
				return false
			})
			// The host recovers; an unrelated change offers the same, undelivered wake again.
			failing.Store(false)
			offered := len(wakes.all())
			if err := rig.registry.Install(&durable.Extension{Name: "unrelated"}); err != nil {
				t.Fatal(err)
			}
			pollUntil(t, "the wake offered again", func() bool { return len(wakes.all()) > offered })
			if last, _ := wakes.last(); !wakeIs(last, false, 2000) {
				t.Fatalf("last wake %s, want {active: false, at: 2000}", showWake(last))
			}
			clock.set(2000)
			h.Resume()
			waitFinished(t, h, id)
			pollUntil(t, "nothing left to do", func() bool { last, ok := wakes.last(); return ok && wakeIs(last, false) })
		})
	})
}
