// A sleep committed while the wake recomputation is queued, which comes due before that recomputation runs, still gets
// its wake. Upstream's recomputation is a microtask (scheduler.ts #scheduleWake) that reads the wake, and so the clock,
// in the turn of the change; here it is a goroutine that can run milliseconds later, after a reservation pass that
// the change queued found the sleep not yet due and dropped it from the candidates. A recomputation that read the clock
// of its own run found the sleep due, armed no timer and offered no alarm, and nothing ever ran the task again: a Wait
// on the submission it settles blocked for good (a retry backoff of 1 ms was the case that hung CI).
//
// Each test holds the recomputation's goroutine, as goroutine scheduling under load can, moves the clock past the sleep
// and releases it.

package harness

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// hostTimers is a HarnessOptions.SetTimer that records the timers it is asked for, so a test fires them itself.
type hostTimers struct {
	mu    sync.Mutex
	armed []armedTimer
}

type armedTimer struct {
	callback   func()
	delayMs    float64
	background bool
}

func (timers *hostTimers) set(callback func(), delayMs float64, background bool) func() {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	timers.armed = append(timers.armed, armedTimer{callback: callback, delayMs: delayMs, background: background})
	return func() {}
}

// foreground returns the callbacks of the foreground timers armed with delayMs.
func (timers *hostTimers) foreground(delayMs float64) []func() {
	timers.mu.Lock()
	defer timers.mu.Unlock()
	var callbacks []func()
	for _, timer := range timers.armed {
		if !timer.background && timer.delayMs == delayMs {
			callbacks = append(callbacks, timer.callback)
		}
	}
	return callbacks
}

// stallWake holds the goroutines that recompute the scheduler's wake once the one queued before has finished; the next
// change queues a recomputation that waits until the returned release runs, and changes until then share it.
func stallWake(t *testing.T, scheduler *TaskScheduler) (release func()) {
	t.Helper()
	eventually(t, func() bool {
		scheduler.mu.Lock()
		defer scheduler.mu.Unlock()
		return !scheduler.wakeScheduled
	})
	scheduler.wakeMu.Lock()
	var once sync.Once
	release = func() { once.Do(scheduler.wakeMu.Unlock) }
	t.Cleanup(release)
	return release
}

// schedulerAtRest reports no reservation pass queued or running, no invocation, and no task left a candidate: each
// sleeping task was found not yet due by the last pass, which dropped it.
func schedulerAtRest(scheduler *TaskScheduler) bool {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return !scheduler.draining && !scheduler.dirty && len(scheduler.invocations) == 0 && scheduler.runnable.Len() == 0 && scheduler.reconciles == 0
}

// sleeperTask sleeps until `until`, then completes.
func sleeperTask(until float64) durable.AnyTask {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, stepState, durable.JsonValue, any]{
		Name:    "test.sleeper",
		Version: 1,
		Initial: func(durable.JsonValue) stepState { return stepState{Phase: "sleep"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, stepState, durable.JsonValue, any]{
			"sleep": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
					return sleepingState[stepState, durable.JsonValue](stepState{Phase: "wake"}, until), nil
				})
			},
			"wake": func(ctx context.Context, _ stepRecord, runtime stepRuntime) error { return tkComplete(ctx, runtime) },
		},
		Abort: func(ctx context.Context, _ stepRecord, runtime stepRuntime) error {
			return runtime.Commit(ctx, func(durable.Tx, stepRecord) (*durable.NextTaskState[stepState, durable.JsonValue], error) {
				return abortedWith[stepState, durable.JsonValue]("test"), nil
			})
		},
	}).Erased()
}

// sleepsAtRest starts the sleeper while the wake recomputation is held, and returns once its sleep is committed and the
// last pass dropped it, with the clock still before it.
func sleepsAtRest(t *testing.T, opened tkOpened, scheduler *TaskScheduler, sleeper durable.AnyTask) durable.TaskId {
	t.Helper()
	opened.harness.Resume()
	scheduler.mu.Lock()
	queued := scheduler.wakeScheduled
	scheduler.mu.Unlock()
	if !queued {
		t.Fatal("the change queued no wake recomputation")
	}
	id := tkStart(t, opened.root, sleeper)
	eventually(t, func() bool {
		record, err := opened.harness.GetTask(testContext, id)
		return err == nil && record != nil && record.State.Status == durable.TaskSleeping && schedulerAtRest(scheduler)
	})
	return id
}

func TestWakeRecomputedAfterSleepCameDueArmsItsTimer(t *testing.T) {
	clock := &clockAt{}
	clock.set(1_000)
	timers := &hostTimers{}
	sleeper := sleeperTask(1_500)
	opened := tkOpenRoot(t, []durable.AnyTask{sleeper}, tkOptions{now: clock.now, setTimer: timers.set})
	scheduler := opened.harness.(*harnessImpl).tasks
	release := stallWake(t, scheduler)
	id := sleepsAtRest(t, opened, scheduler, sleeper)

	clock.set(2_000)
	release()

	// Armed for the sleep, which was ahead at the change: 500 ms before the clock the recomputation ran at.
	var wake []func()
	eventually(t, func() bool {
		wake = timers.foreground(1_500 - 2_000)
		return len(wake) > 0
	})
	wake[0]()
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	settled, err := opened.harness.WaitForTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if settled.State.Outcome == nil || settled.State.Outcome.Status != durable.OutcomeCompleted {
		t.Fatalf("outcome %+v, want completed", settled.State.Outcome)
	}
	mustClose(t, opened.harness)
}

func TestWakeRecomputedAfterSleepCameDueOffersItsAlarm(t *testing.T) {
	clock := &clockAt{}
	clock.set(1_000)
	var mu sync.Mutex
	var wakes []Wake
	sleeper := sleeperTask(1_500)
	opened := tkOpenRoot(t, []durable.AnyTask{sleeper}, tkOptions{now: clock.now, onWake: func(wake Wake) {
		mu.Lock()
		defer mu.Unlock()
		wakes = append(wakes, wake)
	}})
	scheduler := opened.harness.(*harnessImpl).tasks
	release := stallWake(t, scheduler)
	sleepsAtRest(t, opened, scheduler, sleeper)

	clock.set(2_000)
	release()

	// The host is told to wake at the sleep, as upstream tells it in the turn of the change; read at the later clock the
	// sleep is due, and the host would be told only that work is active, which no pass is queued to run.
	alarm := 1_500.0
	want := Wake{Active: false, At: &alarm}
	eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(wakes) > 0 && equalWake(wakes[len(wakes)-1], want)
	})
	mustClose(t, opened.harness)
}

func TestGenerationRetryWaitSettlesWhenWakeRecomputedAfterBackoffCameDue(t *testing.T) {
	clock := &clockAt{}
	clock.set(1_000_000)
	timers := &hostTimers{}
	setup := chatSetup(t)
	setup.SetNow(clock.now)
	setup.Faux.SetResponses([]ai.FauxResponseStep{error503(), fauxAnswer("recovered")})
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(3), BaseDelayMs: new(1_000)}
	})
	harness, root := openChat(t, storage.NewMemoryStorage(), setup, openChatOptions{setTimer: timers.set})
	scheduler := harness.(*harnessImpl).tasks
	release := stallWake(t, scheduler)
	harness.Resume()
	submission := submitInput(t, root, "hi")
	id := conversationTasks(t, harness, root)[0].Id
	eventually(t, func() bool {
		record, err := harness.GetTask(testContext, id)
		return err == nil && record != nil && record.State.Status == durable.TaskSleeping && schedulerAtRest(scheduler)
	})

	// The backoff is 1 s from the clock the failed attempt read; the recomputation runs after it elapsed.
	clock.set(1_002_000)
	release()

	var wake []func()
	eventually(t, func() bool {
		wake = timers.foreground(1_001_000 - 1_002_000)
		return len(wake) > 0
	})
	wake[0]()
	ctx, cancel := context.WithTimeout(testContext, 10*time.Second)
	defer cancel()
	settled, err := submission.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectSettled(t, settled, durable.SubmissionDone, "")
	closeHarness(t, harness)
}
