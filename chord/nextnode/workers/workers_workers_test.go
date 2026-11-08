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
)

// pi: packages/chord/src/next-node/workers/workers.ts
// Ports packages/chord/test/next-node/workers/workers.test.ts (the parts that need no rollout, and no blocked worker): starts
// on demand (the link made at the fork, `started` on `connect`, the start timer covering only the start), failures that
// stay (a crash, a `fatal`, a failed start: no fork for a new hold, holders `failed { waiting }`) until `retry` or a
// rollout, a running worker's lost link recovered from once a minute (workers_blocked_unix_test.go), idle stops and `busy`,
// `stop` and its suspension, the per-address queue, `close`, stale lock files cleared, nothing kept of 1,000 addresses, and
// the worker base (designed out: docs/parity/divergences; a broken entry rejects its creation). Each Pi test is the Go test of the same name's words, with the same inputs and expectations.

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
					t.Error(err)
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

// Pi's `describe('the worker base (decision "Workers start from the base code the server booted with")')`: its first test
// (an edit of the entry's imported source after the component was created runs in no worker, `workers.inputs` names the
// bundled files, the bundle's file is removed by `close`) is a property of Pi's esbuild bundle of a Node entry; a Go worker
// is this program's own compiled executable, which holds all of its code already (no counterpart, see D-WORKER-BASE in
// docs/parity/divergences).

func TestTheWorkerBaseABrokenEntryRejectsCreateWorkersNothingIsRoutedTheKindCanBeRoutedAgain(t *testing.T) {
	dir := t.TempDir()
	serverAddress := fixtureServer.Address(nil)
	server, err := host.CreateHost(host.HostOptions{Address: &serverAddress, Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	plugs := newPlugins(t, dir)
	options := WorkersOptions{
		Kind:      "worker",
		Entry:     filepath.Join(dir, "no-such-worker"),
		Env:       fixtureEnvironment("worker"),
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Principal: func(contracts.Address) (data.Principal, error) { return delta.JsonObjectOf("kind", "server"), nil },
		Start:     func(contracts.Address, context.Context) (delta.JsonValue, error) { return delta.JsonObjectOf(), nil },
		Lock:      func(address contracts.Address) (string, error) { return "chord-test:" + dir + ":" + address.Key, nil },
		Plugins:   plugs,
		OnError:   func(WorkersError) {},
	}
	if _, err := CreateWorkers(server, fixtureSession, options, background); err == nil {
		t.Fatal("CreateWorkers with an entry that is no program: no error")
	}
	options.Entry = fixtureEntry(t)
	workers, err := CreateWorkers(server, fixtureSession, options, background)
	if err != nil {
		t.Fatalf("CreateWorkers after the failed one: %v", err)
	}
	if err := workers.Close(background); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(background); err != nil {
		t.Fatal(err)
	}
}

// Pi's `describe('stale lock files (macOS, the BSDs; decision "Stale lock files are cleared by the starter")')`: a start
// clears the lock file a killed worker of an earlier server left: the worker runs.
func TestStaleLockFilesAStartClearsTheLockFileAKilledWorkerOfAnEarlierServerLeftTheWorkerRuns(t *testing.T) {
	f := newFleet(t, fleetOptions{})
	// What an earlier server's worker SIGKILLed while holding the lock leaves (nothing on Linux: an abstract socket).
	location, err := lockLocationOf("chord-test:" + f.root + ":" + session("S1").Key)
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
