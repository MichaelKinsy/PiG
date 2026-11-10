package workers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode/plugins/model"
)

// pi: packages/chord/src/node/workers/workers.ts
// Ports packages/chord/test/node/workers/workers.test.ts (the parts that need no rollout, and no blocked worker): starts
// on demand (the link made at the fork, `started` on `connect`, the start timer covering only the start), failures that
// stay (a crash, a `fatal`, a failed start: no fork for a new hold, holders `failed { waiting }`) until `retry` or a
// rollout, a running worker's lost link recovered from once a minute (workers_blocked_unix_test.go), idle stops and `busy`,
// `stop` and its suspension, the per-address queue, `close`, stale lock files cleared, nothing kept of 1,000 addresses, and
// the worker base (a private copy of the entry made by its build, which a start waits for and fails with while it failed). Each Pi test is the Go test of the same name's words, with the same inputs and expectations.

func requireCode(t *testing.T, err error, code data.ChordErrorCode, reason data.ChordErrorReason) {
	t.Helper()
	chordErr, ok := data.IsChordErrorCode(err, code)
	if !ok || chordErr.Reason != reason {
		t.Fatalf("error = %#v, want a ChordError %s/%s", err, code, reason)
	}
}

// failedCause is `notes.state.value` of a failed reference: its cause.
func failedCause(t *testing.T, reference *host.Reference) data.FailureCause {
	t.Helper()
	state := reference.State().Value()
	if state.Kind != host.RefStateFailed {
		t.Fatalf("state = %s, want failed", state.Kind)
	}
	return state.Cause
}

func TestStartsOnDemandStartsAWorkerForTheFirstHoldStartingThenRunningItsLinkBindsTheHolder(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	// A worker of this speed may be running before a poll sees it: the state's own frames are what tells starting first.
	statuses := f.watchStatuses("S1")
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return f.stateOf("S1") != nil })
	until(t, func() bool { return len(statuses.all()) > 0 })
	if first := statuses.all()[0]; first != WorkerStarting {
		t.Fatalf("first status = %s, want starting", first)
	}
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if label := labelOf(t, notes); label != "S1 v1" {
		t.Fatalf("label = %q", label)
	}
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerRunning })
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v", starts)
	}
}

func TestStartsOnDemandBindsThroughALoadLongerThanTheHeartbeatTheLinkIsMadeAtTheForkTheWorkerConnectsFirst(t *testing.T) {
	ping, closeAfter := 50*time.Millisecond, 200*time.Millisecond
	f := newFleet(t, fleetOptions{heartbeat: &host.HeartbeatOptions{Ping: &ping, Close: &closeAfter}})
	f.behaviours.set("S1", workerBehaviour{LoadMs: 600})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	if label := labelOf(t, notes); label != "S1 v1" {
		t.Fatalf("label = %q", label)
	}
}

func TestFailuresStayAStartThatFailsFailsTheAddressHoldersFailWaitingWithItANewHoldForksNothingRetryStartsIt(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	f := newFleet(t, fleetOptions{start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
		if fail.Load() {
			return nil, errors.New("no data dir")
		}
		return delta.JsonObjectOf(), nil
	}})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
	cause := failedCause(t, notes)
	if cause.Kind != data.FailureWaiting || cause.Error.Code != data.CodeUnavailable || cause.Error.Message != "worker failed to start" {
		t.Fatalf("cause = %+v / %+v", cause, cause.Error)
	}
	if state := f.stateOf("S1"); state == nil || state.Status != WorkerFailed || state.Error.Message != "worker failed to start" {
		t.Fatalf("state = %+v", state)
	}
	again := f.hold(fixtureNotes, "S1")
	defer again.Release()
	until(t, func() bool { return refKind(again) == host.RefStateFailed })
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v, want one", starts)
	}
	// The full error only for the app's logs (decision "Slice 10 fix round, worker errors in public").
	errs := f.errors.all()
	failed, ok := errs[0].(WorkerFailedError)
	if len(errs) != 1 || !ok || failed.Address != "session|S1" || failed.Error.Error() != "no data dir" {
		t.Fatalf("errors = %+v", errs)
	}
	fail.Store(false)
	if err := f.workers.Retry(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return refKind(notes) == host.RefStateLive && refKind(again) == host.RefStateLive })
	if state := f.stateOf("S1"); state != nil && state.Status == WorkerFailed {
		t.Fatal("the address is still failed after retry")
	}
}

func TestFailuresStayACrashAfterReadyStaysFailedWorkerExitedWorkerFailedNothingRestarts(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	control := f.get(fixtureControl, "S1")
	defer control.Release()
	if _, err := control.Call("crash", float64(7), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed })
	if message := f.stateOf("S1").Error.Message; message != "worker exited (code 7)" {
		t.Fatalf("message = %q", message)
	}
	if kinds := f.kinds(); !reflect.DeepEqual(kinds, []string{"worker_exited", "worker_failed"}) {
		t.Fatalf("kinds = %v", kinds)
	}
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
	time.Sleep(200 * time.Millisecond)
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v, nothing restarts", starts)
	}
}

func TestFailuresStayAFatalCrossesRedactedTheSecretNeverReachesThePublicState(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{Fatal: "boom secret-token-123"})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed })
	if message := f.stateOf("S1").Error.Message; message != "boom [redacted]" {
		t.Fatalf("message = %q", message)
	}
	// Public: the state; the report's stderr tail is the app's to redact (a later test).
	if strings.Contains(f.stateText(), "secret") {
		t.Fatalf("the state shows the secret: %s", f.stateText())
	}
	failure, ok := f.firstFailure()
	if !ok || failure.Error.Error() != "boom [redacted]" {
		t.Fatalf("worker_failed = %+v", failure)
	}
}

func TestFailuresStayAnExitBeforeStartedFailsTheStartAWorkerThatNeverConnectsIsKilledAtTheStartTimer(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	three := 3
	f.behaviours.set("S1", workerBehaviour{Exit: &three})
	exited := f.hold(fixtureNotes, "S1")
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed })
	if message := f.stateOf("S1").Error.Message; message != "worker failed to start" {
		t.Fatalf("message = %q", message)
	}
	failure, _ := f.firstFailure()
	if !strings.Contains(failure.Error.Error(), "exited before it was ready (code 3)") {
		t.Fatalf("worker_failed = %v", failure.Error)
	}
	exited.Release()
	fake, restore := f.clock.useFakeTimers()
	defer restore()
	f.behaviours.set("S2", workerBehaviour{NoConnect: true})
	silent := f.hold(fixtureNotes, "S2")
	until(t, func() bool { return f.stateOf("S2") != nil && f.stateOf("S2").Status == WorkerStarting }, 5_000)
	fake.advance(60 * time.Second)
	until(t, func() bool { return f.stateOf("S2") != nil && f.stateOf("S2").Status == WorkerFailed }, 5_000)
	if message := f.stateOf("S2").Error.Message; message != "worker failed to start" {
		t.Fatalf("message = %q", message)
	}
	last, _ := f.lastFailure()
	if !strings.Contains(last.Error.Error(), "did not start within 60 s") {
		t.Fatalf("worker_failed = %v", last.Error)
	}
	silent.Release()
}

func TestFailuresStayTheStartTimerCoversOnlyTheStartASlowLoadAfterConnectingIsNotKilled(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	fake, restore := f.clock.useFakeTimers()
	defer restore()
	f.behaviours.set("S1", workerBehaviour{Hang: true})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerStarting }, 5_000)
	// The worker's `started` reached the server: the start timer is gone (Pi's `setImmediate` turn, a happens-before here).
	until(t, func() bool { return f.childStarted("S1") }, 5_000)
	fake.advance(120 * time.Second)
	if status := f.stateOf("S1").Status; status != WorkerStarting {
		t.Fatalf("status = %s, want starting", status)
	}
	if kind := refKind(notes); kind != host.RefStateLoading {
		t.Fatalf("notes state = %s, want loading", kind)
	}
}

func TestKeepAliveStopsAWorkerIdleSinceIdleMsAHoldKeepsOneRunningBusyKeepsItToo(t *testing.T) {
	f := newFleet(t, fleetOptions{idleMs: 200})
	held := f.get(fixtureNotes, "held")
	released := f.get(fixtureNotes, "free")
	control := f.get(fixtureControl, "busy")
	if _, err := control.Call("busy", float64(1_500), background); err != nil {
		t.Fatal(err)
	}
	control.Release()
	released.Release()
	until(t, func() bool { return f.stateOf("free") == nil }, 5_000)
	time.Sleep(600 * time.Millisecond)
	if state := f.stateOf("held"); state == nil || state.Status != WorkerRunning {
		t.Fatalf("held = %+v, want running", state)
	}
	if state := f.stateOf("busy"); state == nil || state.Status != WorkerRunning {
		t.Fatalf("busy = %+v, want running", state)
	}
	until(t, func() bool { return f.stateOf("busy") == nil }, 5_000)
	held.Release()
}

func TestKeepAlive1000SequentialLocalCallsInAWorkerNobodyHoldsSendNoMessage(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{LocalCalls: 1_000})
	// Released at once: the calls run 300 ms after the load, with no hold arriving (no reference keeps it busy).
	f.get(fixtureNotes, "S1").Release()
	until(t, func() bool {
		for _, line := range f.output.all() {
			if strings.HasPrefix(line, "session|S1 local calls sent") {
				return true
			}
		}
		return false
	})
	found := false
	for _, line := range f.output.all() {
		found = found || line == "session|S1 local calls sent 0"
	}
	if !found {
		t.Fatalf("output = %v, want local calls sent 0", f.output.all())
	}
}

func TestStopSuspendsWhileHeldHoldersFailWaitingSuspendedNothingStartsForgottenWithTheLastHolder(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	notes := f.get(fixtureNotes, "S1")
	if err := f.workers.Stop(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	if state := f.stateOf("S1"); state != nil {
		t.Fatalf("state = %+v, want none", state)
	}
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
	cause := failedCause(t, notes)
	if cause.Kind != data.FailureWaiting || cause.Error.Message != "session|S1: suspended" {
		t.Fatalf("cause = %+v / %v", cause, cause.Error)
	}
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v", starts)
	}
	notes.Release()
	time.Sleep(20 * time.Millisecond)
	again := f.get(fixtureNotes, "S1")
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1", "session|S1"}) {
		t.Fatalf("starts = %v", starts)
	}
	again.Release()
}

// Forgotten is how an app ends the per-address bookkeeping it made at Start: called once, for the address, when it has no worker, no
// operation and no failure left: not while its worker runs, not while the address is failed.
func TestForgottenRunsOnceWhenTheAddressHasNothingLeftNotWhileItsWorkerRuns(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	held := f.get(fixtureNotes, "F1")
	if got := f.forgotten.all(); len(got) != 0 {
		t.Fatalf("forgotten while the worker runs = %v", got)
	}
	if err := f.workers.Stop(session("F1"), background); err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return len(f.forgotten.all()) > 0 })
	held.Release()
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if got := f.forgotten.all(); !reflect.DeepEqual(got, []string{"session|F1"}) {
		t.Fatalf("forgotten = %v, want once", got)
	}
}

func TestForgottenDoesNotRunForAnAddressThatFailedUntilItsFailureEnds(t *testing.T) {
	f := newFleet(t, fleetOptions{start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
		return nil, errors.New("no start")
	}})
	held := f.hold(fixtureNotes, "F2")
	defer held.Release()
	until(t, func() bool { return f.stateOf("F2") != nil && f.stateOf("F2").Status == WorkerFailed })
	if got := f.forgotten.all(); len(got) != 0 {
		t.Fatalf("forgotten while failed = %v", got)
	}
}

func TestStopAStopDuringAStartWaitsForItThenStopsAStartDuringAStopWaitsForItsExit(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{LoadMs: 300})
	notes := f.hold(fixtureNotes, "S1")
	until(t, func() bool { return f.stateOf("S1") != nil })
	if err := f.workers.Stop(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	if state := f.stateOf("S1"); state != nil {
		t.Fatalf("state = %+v, want none", state)
	}
	notes.Release()
	time.Sleep(20 * time.Millisecond)
	f.behaviours.set("S1", workerBehaviour{})
	again := f.get(fixtureNotes, "S1")
	stopped := make(chan error, 1)
	go func() { stopped <- f.workers.Stop(session("S1"), background) }()
	again.Release()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	third := f.get(fixtureNotes, "S1")
	if starts := f.starts.all(); len(starts) != 3 {
		t.Fatalf("starts = %v, want 3", starts)
	}
	third.Release()
}

func TestStopRunsTheWorkersClosersInReverseOrderAtItsStop(t *testing.T) {
	dir := t.TempDir()
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{Log: filepath.Join(dir, "log"), CloseMs: 50})
	notes := f.get(fixtureNotes, "S1")
	notes.Release()
	if err := f.workers.Stop(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "store\nhost\n" {
		t.Fatalf("log = %q", text)
	}
}

func TestCloseCancelsAStartWaitingInStartAddressNothingForksNewStartsRejectNoWorkerFailedAtShutdown(t *testing.T) {
	var waiting atomic.Bool
	f := newFleet(t, fleetOptions{start: func(address contracts.Address, ctx context.Context) (delta.JsonValue, error) {
		if address.Key == "session|wait" {
			waiting.Store(true)
			<-ctx.Done()
			return nil, context.Cause(ctx)
		}
		return delta.JsonObjectOf(), nil
	}})
	running := f.get(fixtureNotes, "S1")
	waits := f.hold(fixtureNotes, "wait")
	until(t, waiting.Load)
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if text := f.stateText(); text != `{"v":{}}` {
		t.Fatalf("state = %s, want {}", text)
	}
	if errs := f.errors.all(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	late := f.hold(fixtureNotes, "late")
	until(t, func() bool { return refKind(late) == host.RefStateFailed })
	requireCode(t, failedCause(t, late).Error, data.CodeReleased, data.ReasonClosed)
	for _, reference := range []*host.Reference{running, waits, late} {
		reference.Release()
	}
}

func TestFixRound2AThrowingPrincipalHookFailsTheStartWithThePublicMessageTheErrorStaysPrivate(t *testing.T) {
	f := newFleet(t, fleetOptions{principal: func() (data.Principal, error) {
		return nil, errors.New("secret token=sk-1 at /home/owner/private")
	}})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
	state := notes.State().Value()
	if strings.Contains(fmt.Sprint(state.Cause.Error.Message), "secret") || state.Cause.Error.Message != "worker failed to start" {
		t.Fatalf("cause = %+v", state.Cause.Error)
	}
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed })
	if message := f.stateOf("S1").Error.Message; message != "worker failed to start" {
		t.Fatalf("state message = %q", message)
	}
	// Opus's second recheck: the component's own kill of a failed start is no `worker_exited`.
	until(t, func() bool { return len(f.errors.all()) > 0 })
	time.Sleep(50 * time.Millisecond)
	errs := f.errors.all()
	failed, ok := errs[0].(WorkerFailedError)
	if len(errs) != 1 || !ok || failed.Address != "session|S1" || !strings.Contains(failed.Error.Error(), "secret") {
		t.Fatalf("errors = %+v", errs)
	}
}

func TestFixRound2AThrowingAuthorizeRefusesTheHoldForbiddenTheErrorStaysPrivateReportedAuthorizeFailed(t *testing.T) {
	f := newFleet(t, fleetOptions{authorize: func() (bool, error) { return false, errors.New("secret lookup") }})
	remote, err := host.CreateHost(host.HostOptions{OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	up, down := link.MemoryLinkPair()
	if err := f.server.Accept(up, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "guest", "id", "g")}, background); err != nil {
		t.Fatal(err)
	}
	if err := remote.Connect(down, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "server")}, background); err != nil {
		t.Fatal(err)
	}
	loadHost(t, remote)
	notes, err := remote.Hold(background, fixtureNotes, delta.JsonObjectOf("id", "S1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	until(t, func() bool { return refKind(notes) != host.RefStateLoading })
	state := notes.State().Value()
	if state.Kind != host.RefStateFailed || state.Cause.Error.Code != data.CodeForbidden || strings.Contains(state.Cause.Error.Message, "secret") {
		t.Fatalf("state = %+v / %v", state, state.Cause.Error)
	}
	errs := f.errors.all()
	failed, ok := errs[0].(AuthorizeFailedError)
	if len(errs) != 1 || !ok || failed.Address != "session|S1" || len(f.starts.all()) != 0 {
		t.Fatalf("errors = %+v starts = %v", errs, f.starts.all())
	}
	notes.Release()
	if err := remote.Close(background); err != nil {
		t.Fatal(err)
	}
}

func TestFixRound1CloseDuringAForkedStartThatNeverConnectsReturnsAtOnceNoFailure(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{NoConnect: true})
	notes := f.hold(fixtureNotes, "S1")
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerStarting })
	started := time.Now()
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("close took %v", elapsed)
	}
	if errs := f.errors.all(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	notes.Release()
}

func TestFixRound1CloseDuringAnAppsStartThatIgnoresItsSignalReturnsAtOnceNothingForksAfterIt(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	f := newFleet(t, fleetOptions{start: func(contracts.Address, context.Context) (delta.JsonValue, error) {
		<-block
		return nil, errors.New("never")
	}})
	notes := f.hold(fixtureNotes, "S1")
	time.Sleep(50 * time.Millisecond)
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	forked := f.stateOf("S1") != nil
	if errs := f.errors.all(); forked || len(errs) != 0 {
		t.Fatalf("forked = %v errors = %+v", forked, errs)
	}
	notes.Release()
}

// `close` ends its wait with its context (`awaitWithContext(Promise.all(stops), context)`): a worker that ignores `shutdown`
// is left running, and an app hook that ignores its context holds nothing.
func TestCloseStopsWaitingWhenItsContextEndsAWorkerThatIgnoresShutdownKeepsRunning(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{CloseMs: 60_000})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	var exited <-chan struct{}
	f.workers.loop.Run(func() { exited = f.workers.addresses[session("S1").Key].child.process.Exited() })
	ctx, cancel := context.WithTimeout(background, 100*time.Millisecond)
	defer cancel()
	err := f.workers.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close = %v, want its context's deadline", err)
	}
	select {
	case <-exited:
		t.Fatal("Close waited for the worker's exit after its context ended")
	default:
	}
}

func TestCloseStopsWaitingWhenItsContextEndsAnAppHookThatIgnoresItsContextKeepsRunning(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f := newFleet(t, fleetOptions{principal: func() (data.Principal, error) {
		once.Do(func() { close(reached) })
		<-release
		return nil, errors.New("released")
	}})
	defer close(release)
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	<-reached
	ctx, cancel := context.WithTimeout(background, 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.workers.Close(ctx) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v, want its context's deadline", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close ignores its context: it waits for an app hook that never returns")
	}
}

// workerTmp is a temporary directory for the workers of a test, which is not the server's (the test process's) own, and
// a lister of the NodeHost socket directories left in it. It is short: the socket's path in it must fit a unix address.
func workerTmp(t *testing.T) (env map[string]string, socketDirs func() []string) {
	t.Helper()
	scratch, err := os.MkdirTemp("", "w")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scratch) })
	return map[string]string{"TMPDIR": scratch}, func() []string {
		entries, err := os.ReadDir(scratch)
		if err != nil {
			t.Fatal(err)
		}
		var found []string
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "chord-plugins-") {
				found = append(found, entry.Name())
			}
		}
		return found
	}
}

// startingWorker is a fleet whose worker S1 is in the middle of starting its NodeHost: the host's socket directory is made
// (in the worker's own TMPDIR) and its Node subprocess is held back, until the test is over.
func startingWorker(t *testing.T) (f *fleet, socketDirs func() []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a named pipe leaves no directory")
	}
	env, socketDirs := workerTmp(t)
	f = newFleet(t, fleetOptions{env: env})
	gate := filepath.Join(t.TempDir(), "node-may-start")
	f.behaviours.set("S1", workerBehaviour{HoldNodeStart: gate})
	// Whatever the test left running goes on to start its Node host and end.
	t.Cleanup(func() { _ = os.WriteFile(gate, nil, 0o600) })
	notes := f.hold(fixtureNotes, "S1")
	t.Cleanup(notes.Release)
	until(t, func() bool { return len(socketDirs()) > 0 })
	if state := f.stateOf("S1"); state == nil || state.Status != WorkerStarting {
		t.Fatalf("worker = %+v, want one starting its host", state)
	}
	return f, socketDirs
}

// Not upstream's: Pi evaluates plugins in-process, so it has no socket directory to leave behind. PiG's NodeHost listens on a
// socket in a directory of its own, and a worker that ends while that host is still starting (stopped by Close here, which
// asks it to shut down) has no host to close it: the directory must not outlive the worker, whichever temporary directory
// the worker has.
func TestCloseLeavesNoSocketDirectoryOfAWorkersNodeHostThatWasStillStarting(t *testing.T) {
	f, socketDirs := startingWorker(t)
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if left := socketDirs(); len(left) != 0 {
		t.Fatalf("left behind by the stopped worker: %v", left)
	}
}

// The owner variable is the server's to tell the worker who started it: the worker takes it out of its environment at start, as
// it does the channel's, so no program it spawns sees it.
func TestAWorkerDoesNotPassItsSocketOwnerToTheProgramsItSpawns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the probe is a /bin/sh program")
	}
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{SpawnProbe: true})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	const prefix = "session|S1 owner seen by a spawned program: "
	var line string
	until(t, func() bool {
		for _, output := range f.output.all() {
			if strings.HasPrefix(output, prefix) {
				line = output
				return true
			}
		}
		return false
	})
	if line != prefix+"unset" {
		t.Fatalf("output = %q, want the variable unset", line)
	}
}

// The same for a worker that is killed (Pi's `child.process.kill()` of a failed start, a crash, the start timeout), which has no
// chance to clean up at all: the server removes what its process left, once it is gone.
func TestAWorkerKilledWhileItsNodeHostIsStartingLeavesNoSocketDirectory(t *testing.T) {
	f, socketDirs := startingWorker(t)
	process, err := os.FindProcess(f.stateOf("S1").Pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	// The server's state follows the exit, which comes after the removal.
	until(t, func() bool { state := f.stateOf("S1"); return state != nil && state.Status == WorkerFailed })
	if left := socketDirs(); len(left) != 0 {
		t.Fatalf("left behind by the killed worker: %v", left)
	}
}

// The context a start gives the app's `start` hook is derived from the route's and from Close's, which keep a registration
// of it until it is cancelled: it ends with the start, or a long-lived server keeps one pair per start for good.
func TestAStartsContextEndsWithTheStartSoItsRegistrationsDoNotAccumulate(t *testing.T) {
	var given atomic.Pointer[context.Context]
	f := newFleet(t, fleetOptions{start: func(_ contracts.Address, ctx context.Context) (delta.JsonValue, error) {
		given.Store(&ctx)
		return delta.JsonObjectOf(), nil
	}})
	notes := f.get(fixtureNotes, "S1")
	defer notes.Release()
	ctx := given.Load()
	if ctx == nil {
		t.Fatal("start never ran")
	}
	until(t, func() bool { return (*ctx).Err() != nil }, 2_000)
}

func TestFixRound1AStopDuringTheWorkersStartupIsAStopItsClosersRunNothingFails(t *testing.T) {
	dir := t.TempDir()
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{LoadMs: 5_000, CloseMs: 200, Log: filepath.Join(dir, "log")})
	notes := f.hold(fixtureNotes, "S1")
	until(t, func() bool { return f.stateOf("S1") != nil })
	time.Sleep(300 * time.Millisecond)
	if err := f.workers.Stop(session("S1"), background); err != nil {
		t.Fatal(err)
	}
	if errs := f.errors.all(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	text, err := os.ReadFile(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "store\nhost\n" {
		t.Fatalf("log = %q", text)
	}
	notes.Release()
}

func TestFixRound1KeepsAWorkersStderrOutOfThePublicStateOnlyWorkerFailedCarriesItsTail(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	f.behaviours.set("S1", workerBehaviour{Fatal: "boom secret-token-123"})
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return f.stateOf("S1") != nil && f.stateOf("S1").Status == WorkerFailed })
	if strings.Contains(f.stateText(), "secret") {
		t.Fatalf("the state shows the secret: %s", f.stateText())
	}
	if text := notes.State().Value(); text.Cause.Error != nil && strings.Contains(text.Cause.Error.Message, "secret") {
		t.Fatalf("the reference shows the secret: %v", text.Cause.Error)
	}
	failure, ok := f.firstFailure()
	if !ok || !strings.Contains(failure.StderrTail, "secret-token-123") {
		t.Fatalf("worker_failed = %+v", failure)
	}
}

func TestGC100AddressesStartedAndStoppedLeaveNoStateNoRecordNoSuspension(t *testing.T) {
	var mu sync.Mutex
	var refs []weak.Pointer[delta.JsonObject]
	f := newFleet(t, fleetOptions{start: func(address contracts.Address, _ context.Context) (delta.JsonValue, error) {
		mu.Lock()
		refs = append(refs, weak.Make(address.Params))
		mu.Unlock()
		return delta.JsonObjectOf(), nil
	}})
	// Twenty at a time: each a real fork, start, hold, stop and exit.
	for batch := range 5 {
		var group sync.WaitGroup
		for index := range 20 {
			group.Go(func() {
				id := fmt.Sprintf("R%d", batch*20+index)
				notes, err := f.server.Get(background, fixtureNotes, delta.JsonObjectOf("id", id), host.GetOptions{})
				if err != nil {
					t.Errorf("%v; what the workers reported: %s", err, f.reportedErrors())
					return
				}
				notes.Release()
				if err := f.workers.Stop(session(id), background); err != nil {
					t.Error(err)
				}
			})
		}
		group.Wait()
	}
	if text := f.stateText(); text != `{"v":{}}` {
		t.Fatalf("state = %s", text)
	}
	mu.Lock()
	collected := append([]weak.Pointer[delta.JsonObject](nil), refs...)
	mu.Unlock()
	expectCollected(t, collected...)
}

func TestGC1000FailedAndRetriedAddressesLeaveNoStateNoRecordAndNoSuspension(t *testing.T) {
	var mu sync.Mutex
	var refs []weak.Pointer[delta.JsonObject]
	f := newFleet(t, fleetOptions{start: func(address contracts.Address, _ context.Context) (delta.JsonValue, error) {
		mu.Lock()
		refs = append(refs, weak.Make(address.Params))
		mu.Unlock()
		return nil, errors.New("no worker")
	}})
	for index := range 1_000 {
		id := fmt.Sprintf("A%d", index)
		ref := f.hold(fixtureNotes, id)
		until(t, func() bool { return refKind(ref) == host.RefStateFailed })
		ref.Release()
		if err := f.workers.Stop(session(id), background); err != nil {
			t.Fatal(err)
		}
		if err := f.workers.Retry(session(id), background); err != nil {
			t.Fatal(err)
		}
	}
	if text := f.stateText(); text != `{"v":{}}` {
		t.Fatalf("state = %s", text)
	}
	mu.Lock()
	collected := append([]weak.Pointer[delta.JsonObject](nil), refs[:50]...)
	mu.Unlock()
	expectCollected(t, collected...)
}

// Pi's `describe('the worker base (decision "Workers start from the base code the server booted with")')`: Pi's base is a bundle of the
// entry's imported source, built in memory and written at the first fork. A Go worker's is a private copy of the entry executable,
// made by Build (a program cannot be held in memory), which every worker forks and Close removes.

// copiedEntry is a copy of the fixture's worker program in a new directory: its path.
func copiedEntry(t *testing.T) string {
	t.Helper()
	original, err := os.ReadFile(fixtureEntry(t))
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(t.TempDir(), "entry"+filepath.Ext(fixtureEntry(t)))
	if err := os.WriteFile(entry, original, 0o755); err != nil {
		t.Fatal(err)
	}
	return entry
}

// forkedFrom is the path the worker of id S1 ran from (what the fixture reports with fixtureReportExecutable).
func forkedFrom(f *fleet) string {
	var from string
	for _, line := range f.output.all() {
		if path, ok := strings.CutPrefix(line, "session|S1 executable "); ok {
			from = path
		}
	}
	return from
}

// TestTheWorkerBaseAWorkerStartedAfterAnEditOfTheEntrysExecutableRunsTheCodeTheBaseWasBuiltWith runs `it("a worker started after an
// edit of the entry's imported source runs the code the base was built with")`. `inputs` (packages/chord/src/node/workers/workers.ts:256,
// workers.test.ts:75) lists the files the base was built from, so that an edit of any of them is detected: here, with a Go worker's base
// being one copy of its executable (a Go worker is a program, not a Node bundle), the entry with its bytes' hash.
func TestTheWorkerBaseAWorkerStartedAfterAnEditOfTheEntrysExecutableRunsTheCodeTheBaseWasBuiltWith(t *testing.T) {
	entry := copiedEntry(t)
	f := newFleet(t, fleetOptions{entry: entry, env: map[string]string{fixtureReportExecutable: "1"}})
	if f.workers.Built() != nil {
		t.Fatal("built before anything asked for it")
	}
	built := f.workers.Build()
	resolved, err := filepath.Abs(entry)
	if err != nil {
		t.Fatal(err)
	}
	if want, ok := built.Inputs[resolved]; built.Err != nil || !ok || len(built.Inputs) != 1 || want != model.ContentHash(mustRead(t, entry)) {
		t.Fatalf("build = %+v, want the entry with its bytes' hash", built)
	}
	if inputs := f.workers.Inputs(); len(inputs) != 1 || inputs[resolved] != built.Inputs[resolved] {
		t.Fatalf("Inputs() = %v, want the files the base was built from: %v", inputs, built.Inputs)
	}
	// Half way through an edit: the program on disk is no longer one that runs.
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("not a program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := f.hold(fixtureNotes, "S1")
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if label := labelOf(t, notes); label != "S1 v1" {
		t.Fatalf("label = %q, want S1 v1", label)
	}
	from := forkedFrom(f)
	if from == "" || from == entry {
		t.Fatalf("the worker ran from %q, want the base's copy (not %q)", from, entry)
	}
	if _, err := os.Stat(from); err != nil {
		t.Fatalf("the worker's file is gone while the component lives: %v", err)
	}
	notes.Release()
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Fatalf("the worker's file after Close: %v, want it removed", err)
	}
}

// listDir is the names in dir.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Pi's `writes nothing before the first file(): a handle never forked leaves a named directory as it is, also at remove`: a
// component that closed before its build copies nothing, and its close leaves the directory as it is (another start's, by then).
func TestTheWorkerBaseAComponentThatClosedBeforeItsBuildWritesNothingAndLeavesANamedDirectoryAsItIs(t *testing.T) {
	named := filepath.Join(t.TempDir(), "bases", "worker")
	if err := os.MkdirAll(named, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(named, "worker")
	if err := os.WriteFile(other, []byte("another start's"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFleet(t, fleetOptions{baseDir: named})
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if built := f.workers.Build(); built.Err == nil {
		t.Fatal("a closed component built")
	}
	if got := mustRead(t, other); string(got) != "another start's" {
		t.Fatalf("the named directory's file = %q after the close, want it as it was", got)
	}
}

// Pi's `in a named directory: emptied first (an earlier build's files go), written again there, removed by remove`.
func TestTheWorkerBaseInANamedDirectoryEmptiedFirstWrittenAgainThereRemovedByClose(t *testing.T) {
	named := filepath.Join(t.TempDir(), "bases", "worker")
	if err := os.MkdirAll(named, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(named, "worker-old"), []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFleet(t, fleetOptions{baseDir: named, env: map[string]string{fixtureReportExecutable: "1"}})
	if built := f.workers.Build(); built.Err != nil {
		t.Fatal(built.Err)
	}
	copied := filepath.Join(named, "worker"+filepath.Ext(fixtureEntry(t)))
	if names := listDir(t, named); len(names) != 1 || filepath.Join(named, names[0]) != copied {
		t.Fatalf("the directory = %v, want only %s", names, copied)
	}
	// Windows reports every directory as 0777 whatever its access list says, so only another system shows the mode.
	if info, err := os.Stat(named); err != nil || !info.IsDir() || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o700) {
		t.Fatalf("the directory = %v, %v, want a directory of mode 0700", info, err)
	}
	// A cleaner of temporary files removed it: written again (the entry has the bytes the base was built with).
	if err := os.Remove(copied); err != nil {
		t.Fatal(err)
	}
	notes := f.hold(fixtureNotes, "S1")
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if from := forkedFrom(f); from != copied {
		t.Fatalf("the worker ran from %q, want %q", from, copied)
	}
	notes.Release()
	if err := f.workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(named); !os.IsNotExist(err) {
		t.Fatalf("the directory after Close: %v, want it removed", err)
	}
}

// Pi's `a start waits for the base's build, and starts it when nothing did`: the worker base's build is the entry's check here.
func TestTheWorkerBaseAStartWaitsForTheBasesBuildAndStartsItWhenNothingDid(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	if f.workers.Built() != nil {
		t.Fatal("built before anything asked for it")
	}
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if built := f.workers.Built(); built == nil || built.Err != nil {
		t.Fatalf("built = %+v, want built", built)
	}
	if starts := f.starts.all(); !reflect.DeepEqual(starts, []string{"session|S1"}) {
		t.Fatalf("starts = %v", starts)
	}
}

// Pi's `a broken entry fails its build and every start (worker_failed, the public start error); nothing forks`: an entry that is no
// program that can be started is the Go worker base that does not build.
func TestTheWorkerBaseABrokenEntryFailsItsBuildAndEveryStartWorkerFailedThePublicStartErrorNothingForks(t *testing.T) {
	f := newFleet(t, fleetOptions{entry: filepath.Join(t.TempDir(), "no-such-worker")})
	if built := f.workers.Build(); built.Err == nil || !strings.Contains(built.Err.Error(), "the worker entry cannot be started") {
		t.Fatalf("build = %+v, want the entry that cannot be started", built)
	}
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return refKind(notes) == host.RefStateFailed })
	cause := failedCause(t, notes)
	if cause.Kind != data.FailureWaiting || cause.Error.Code != data.CodeUnavailable || cause.Error.Message != "worker failed to start" {
		t.Fatalf("cause = %+v / %+v", cause, cause.Error)
	}
	// The app's start data is not asked for: the base fails the start first.
	if starts := f.starts.all(); len(starts) != 0 {
		t.Fatalf("starts = %v, want none", starts)
	}
	errs := f.errors.all()
	failed, ok := errs[0].(WorkerFailedError)
	if len(errs) != 1 || !ok || failed.Address != "session|S1" {
		t.Fatalf("errors = %+v, want one worker_failed for session|S1", errs)
	}
	if state := f.stateOf("S1"); state == nil || state.Status != WorkerFailed {
		t.Fatalf("state = %+v, want failed", state)
	}
}

// Pi's `describe('stale lock files (macOS, the BSDs; decision "Stale lock files are cleared by the starter")')`: a start
// clears the lock file a killed worker of an earlier server left: the worker runs.
func TestStaleLockFilesAStartClearsTheLockFileAKilledWorkerOfAnEarlierServerLeftTheWorkerRuns(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	// What an earlier server's worker SIGKILLed while holding the lock leaves (nothing on Linux: an abstract socket).
	location, err := lockAddress("chord-test:" + f.root + ":" + session("S1").Key)
	if err != nil {
		t.Fatal(err)
	}
	if location.kind == lockFile {
		leaveSocketFile(t, location.address)
	}
	notes := f.hold(fixtureNotes, "S1")
	defer notes.Release()
	until(t, func() bool { return refKind(notes) == host.RefStateLive })
	if state := f.stateOf("S1"); state != nil && state.Status == WorkerFailed {
		t.Fatalf("state = %+v, want not failed", state)
	}
}
