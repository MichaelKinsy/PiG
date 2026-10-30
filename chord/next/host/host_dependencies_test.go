package host

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/test/next/host/dependencies.test.ts for the part that needs no host. Every case of upstream's file
// runs a host (loaded/createHost with defineFacet, host.hold, raw parent peers, relayed hosts): "the dependency check" breaks
// or parks a view object by what its Notes/Ping/Pong dependencies do, "parked_long" counts readyTimeout on a hold that is on
// a link, and "a getter of a parked object" is host.get's. These are own tests of checkDependencies, waitingOn and
// parkedLong with fakes of the seams (a binding and its target, an object and its host), each naming the case whose
// behavior it checks; the host cases stay with the host core and the remote lane.

// fakeTarget is a binding's target.
type fakeTarget struct {
	blocksFirstPass bool
	heldOnLink      bool
	name            string
}

func (t *fakeTarget) BlocksFirstPass() bool { return t.blocksFirstPass }
func (t *fakeTarget) HeldOnLink() bool      { return t.heldOnLink }
func (t *fakeTarget) Label() string         { return t.name }

// fakeBinding is a reference's binding: its state, its label and its target (none: nil).
type fakeBinding struct {
	state *RefState
	name  string
	to    dependencyTarget
}

func (b *fakeBinding) refState() *RefState      { return b.state }
func (b *fakeBinding) label() string            { return b.name }
func (b *fakeBinding) target() dependencyTarget { return b.to }

func referenceDependency(binding *fakeBinding) dependency {
	return dependency{kind: dependencyReference, binding: binding}
}

func dependencyErrorOf(code data.ChordErrorCode, reason data.ChordErrorReason, message string) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: code, Reason: reason, Message: message}, nil)
}

func dependencyFailedWith(kind data.FailureKind, err *data.ChordError) *RefState {
	return &RefState{Kind: RefStateFailed, Cause: data.FailureCause{Kind: kind, Error: err}}
}

func dependencyEndedWith(err *data.ChordError) *RefState {
	return &RefState{Kind: RefStateEnded, Error: err}
}

// Mirrors "the dependency check › breaks a dependent off a cycle with the cycle's error: it would wait on a ring that never
// heals".
func TestTheDependencyCheckBreaksADependentOffACycleWithTheCyclesError(t *testing.T) {
	cycle := dependencyErrorOf(data.CodeInvalid, data.ReasonCycle, "app.ping and app.pong read each other")
	got := checkDependencies([]dependency{
		referenceDependency(&fakeBinding{state: refLive, name: "app.live"}),
		referenceDependency(&fakeBinding{state: dependencyFailedWith(data.FailureBroken, cycle), name: "app.ping"}),
		referenceDependency(&fakeBinding{state: refLoading, name: "app.later"}),
	})
	if got.kind != checkBroken || got.err != cycle {
		t.Fatalf("check = %+v, want broken with the cycle's error", got)
	}
}

// Mirrors "breaks a dependent whose dependency ended (gone): the request cannot come back": an ended dependency breaks, with
// its error, also after a failed one.
func TestTheDependencyCheckBreaksADependentWhoseDependencyEnded(t *testing.T) {
	revoked := dependencyErrorOf(data.CodeForbidden, data.ReasonRevoked, "revoked")
	got := checkDependencies([]dependency{
		referenceDependency(&fakeBinding{state: dependencyFailedWith(data.FailureTransient, dependencyErrorOf(data.CodeUnavailable, "", "offline")), name: "app.first"}),
		referenceDependency(&fakeBinding{state: dependencyEndedWith(revoked), name: "app.notes(S1)"}),
	})
	if got.kind != checkBroken || got.err != revoked {
		t.Fatalf("check = %+v, want broken with the revoked error", got)
	}
}

// Mirrors "breaks a dependent whose dependency a relay's authorize refused: forbidden heals only by retry()": a forbidden
// failure (of any reason) is broken, an unauthorized one, not parked.
func TestTheDependencyCheckBreaksADependentWhoseDependencyWasRefusedAuthority(t *testing.T) {
	forbidden := dependencyErrorOf(data.CodeForbidden, "", "session|S1: not authorized")
	got := checkDependencies([]dependency{referenceDependency(&fakeBinding{state: dependencyFailedWith(data.FailureBroken, forbidden), name: "app.notes(S1)"})})
	if got.kind != checkBroken || got.err != forbidden {
		t.Fatalf("check = %+v, want broken with the forbidden error", got)
	}
	// Another invalid reason is no cycle: it parks.
	args := dependencyErrorOf(data.CodeInvalid, data.ReasonArgs, "bad")
	got = checkDependencies([]dependency{referenceDependency(&fakeBinding{state: dependencyFailedWith(data.FailureBroken, args), name: "app.notes(S1)"})})
	if got.kind != checkParked {
		t.Fatalf("check of an invalid/args failure = %+v, want parked", got)
	}
}

// Mirrors "goes live once every dependency is: one parked on two, the second binding last".
func TestTheDependencyCheckGoesLiveOnceEveryDependencyIs(t *testing.T) {
	first := &fakeBinding{state: refLoading, name: "app.notes(S1)"}
	second := &fakeBinding{state: refLoading, name: "app.notes(S2)"}
	dependencies := []dependency{referenceDependency(first), referenceDependency(second)}
	if got := checkDependencies(dependencies); got.kind != checkParked || got.shown != refLoading || !got.firstPass || got.remoteLoading != nil {
		t.Fatalf("check with two loading = %+v, want parked loading", got)
	}
	first.state = refLive
	if got := checkDependencies(dependencies); got.kind != checkParked || got.shown != refLoading {
		t.Fatalf("check with the first live = %+v, want parked loading", got)
	}
	second.state = refLive
	if got := checkDependencies(dependencies); got.kind != checkLive {
		t.Fatalf("check with both live = %+v, want live", got)
	}
	if got := checkDependencies(nil); got.kind != checkLive {
		t.Fatalf("check of no dependency = %+v, want live", got)
	}
}

// Mirrors "settles a waiting ring across hosts after one round: the root cause kept" and "a getter of a parked object
// rejects with the last parked cause": the first failed dependency in declaration order is the blocker named, as a waiting
// cause of its label and its error; a failed one wins over a loading one.
func TestTheDependencyCheckNamesTheFirstFailedBlocker(t *testing.T) {
	unserved := dependencyErrorOf(data.CodeUnavailable, "", "app.x is not served here")
	other := dependencyErrorOf(data.CodeUnavailable, "", "offline")
	got := checkDependencies([]dependency{
		referenceDependency(&fakeBinding{state: refLoading, name: "app.loading"}),
		referenceDependency(&fakeBinding{state: dependencyFailedWith(data.FailureWaiting, unserved), name: "app.a"}),
		referenceDependency(&fakeBinding{state: dependencyFailedWith(data.FailureTransient, other), name: "app.b"}),
	})
	if got.kind != checkParked || got.shown.Kind != RefStateFailed || got.shown.RetryAt != nil {
		t.Fatalf("check = %+v, want parked failed without retryAt", got)
	}
	want := waitingOn("app.a", unserved)
	if got.shown.Cause.Kind != data.FailureWaiting || got.shown.Cause.Error.Message != want.Cause.Error.Message ||
		got.shown.Cause.Error.Message != "waiting for app.a: app.x is not served here" {
		t.Fatalf("shown cause = %+v, want waiting for app.a: app.x is not served here", got.shown.Cause)
	}
}

// Mirrors the dependency check's `firstPass` and `remoteLoading` (parked_long's "is not reported for a local blocker" and
// "is reported ... on a remote attempt only loading"): a blocker here still without its first outcome keeps the first pass
// open, whether loading or failed; the first remote blocker that is only loading on a link is named.
func TestTheDependencyCheckFirstPassAndRemoteLoading(t *testing.T) {
	local := &fakeBinding{state: refLoading, name: "app.slow(1)", to: &fakeTarget{blocksFirstPass: true, name: "app.slow(1)"}}
	got := checkDependencies([]dependency{referenceDependency(local)})
	if got.kind != checkParked || got.firstPass || got.remoteLoading != nil {
		t.Fatalf("local loading blocker = %+v, want parked, first pass open, no remote", got)
	}
	failedHere := &fakeBinding{state: dependencyFailedWith(data.FailureWaiting, dependencyErrorOf(data.CodeUnavailable, "", "x")), name: "app.f", to: &fakeTarget{blocksFirstPass: true}}
	if got := checkDependencies([]dependency{referenceDependency(failedHere)}); got.firstPass {
		t.Fatalf("failed blocker without a first outcome left the first pass over: %+v", got)
	}
	remote := &fakeBinding{state: refLoading, name: "app.notes(S1)", to: &fakeTarget{heldOnLink: true, name: "app.notes(S1)"}}
	remote2 := &fakeBinding{state: refLoading, name: "app.notes(S2)", to: &fakeTarget{heldOnLink: true, name: "app.notes(S2)"}}
	got = checkDependencies([]dependency{referenceDependency(remote), referenceDependency(remote2)})
	if got.remoteLoading == nil || *got.remoteLoading != "app.notes(S1)" || !got.firstPass {
		t.Fatalf("remote loading blockers = %+v, want the first, app.notes(S1), first pass over", got)
	}
	// A remote blocker whose hold is not on a link (the path has no link) is not named.
	unlinked := &fakeBinding{state: refLoading, name: "app.notes(S1)", to: &fakeTarget{name: "app.notes(S1)"}}
	if got := checkDependencies([]dependency{referenceDependency(unlinked)}); got.remoteLoading != nil {
		t.Fatalf("a hold not on a link was named: %+v", got)
	}
	// A remote blocker that failed is not named either: the parked attempt shows its cause.
	failedRemote := &fakeBinding{state: dependencyFailedWith(data.FailureWaiting, dependencyErrorOf(data.CodeUnavailable, "", "app.notes is not served here")), name: "app.notes(S1)", to: &fakeTarget{heldOnLink: true, name: "app.notes(S1)"}}
	got = checkDependencies([]dependency{referenceDependency(failedRemote)})
	if got.kind != checkParked || got.remoteLoading != nil || got.shown.Kind != RefStateFailed {
		t.Fatalf("failed remote blocker = %+v, want parked failed with no remote loading", got)
	}
}

// Own test of the registry dependencies (`self.registry` values): one served and composed is live, also at its last good
// value while a later composition fails; one nobody serves parks the attempt, waiting, naming the registry.
func TestTheDependencyCheckOfRegistryValues(t *testing.T) {
	e := newModelsEnv(t)
	live := dependency{kind: dependencyRegistry, value: e.source}
	if got := checkDependencies([]dependency{live}); got.kind != checkLive {
		t.Fatalf("check of a served registry = %+v, want live", got)
	}
	e.set(1)
	// The check composes it (a lazy composition) before the failing one.
	if got := checkDependencies([]dependency{live}); got.kind != checkLive {
		t.Fatalf("check of a registry composed to 1 = %+v, want live", got)
	}
	// Another object's models make the composition fail, while the one it was composed from stays in the table.
	e.contribute("@test/other", -5)
	if got := checkDependencies([]dependency{live}); got.kind != checkLive {
		t.Fatalf("check of a registry with a last good value = %+v, want live", got)
	}
	absent := registries.NewRegistryRecord("app.absent", e.batch, func(registries.RegistryError) {})
	got := checkDependencies([]dependency{live, {kind: dependencyRegistry, value: absent.Value(1)}})
	if got.kind != checkParked || got.shown.Kind != RefStateFailed || got.shown.Cause.Kind != data.FailureWaiting || !got.firstPass || got.remoteLoading != nil {
		t.Fatalf("check with an unserved registry = %+v, want parked waiting", got)
	}
	if message := got.shown.Cause.Error.Message; !strings.HasPrefix(message, "waiting for registry app.absent: ") {
		t.Fatalf("waiting message = %q, want it to name registry app.absent", message)
	}
	// A reference that failed before it keeps its place as the blocker named.
	first := &fakeBinding{state: dependencyFailedWith(data.FailureTransient, dependencyErrorOf(data.CodeUnavailable, "", "offline")), name: "app.first"}
	got = checkDependencies([]dependency{referenceDependency(first), {kind: dependencyRegistry, value: absent.Value(1)}})
	if message := got.shown.Cause.Error.Message; message != "waiting for app.first: offline" {
		t.Fatalf("waiting message = %q, want waiting for app.first: offline", message)
	}
}

// Own test of `waitingOn`: a parked attempt's failed state, waiting, with no retry time, and a new object each time.
func TestWaitingOnIsAFailedWaitingStateWithoutRetryAt(t *testing.T) {
	err := dependencyErrorOf(data.CodeUnavailable, "", "offline")
	got := waitingOn("app.notes(S1)", err)
	want := data.WaitingFor("app.notes(S1)", err)
	if got.Kind != RefStateFailed || got.RetryAt != nil || !reflect.DeepEqual(got.Cause, want) {
		t.Fatalf("waitingOn = %+v, want failed waiting %+v", got, want)
	}
}

// fakeParkedObject is the object whose attempt parks, and its host's timeouts.ready and report.
type fakeParkedObject struct {
	name    string
	ready   time.Duration
	reports []HostError
}

func (o *fakeParkedObject) label() string               { return o.name }
func (o *fakeParkedObject) readyTimeout() time.Duration { return o.ready }
func (o *fakeParkedObject) report(err HostError)        { o.reports = append(o.reports, err) }

func parkedLongHarness() (*fakeLoop, *fakeParkedObject, *parkedLong) {
	loop := newFakeLoop()
	object := &fakeParkedObject{name: "app.view(1)", ready: time.Second}
	return loop, object, newParkedLong(loop, object)
}

func dependencyBlocker(label string) *string { return &label }

func parkedLongReport(waitingFor string, since int64) HostError {
	return HostError{Kind: ErrParkedLong, Object: "app.view(1)", WaitingFor: waitingFor, Since: since}
}

// Mirrors "parked_long › is reported once per parking episode, after readyTimeout on a remote attempt only loading; nothing
// fails".
func TestParkedLongIsReportedOncePerParkingEpisodeAfterReadyTimeout(t *testing.T) {
	loop, object, long := parkedLongHarness()
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	loop.Advance(999)
	if len(object.reports) != 0 {
		t.Fatalf("reports before readyTimeout = %v", object.reports)
	}
	loop.Advance(1)
	if want := []HostError{parkedLongReport("app.notes(S1)", 0)}; !reflect.DeepEqual(object.reports, want) {
		t.Fatalf("reports = %v, want %v", object.reports, want)
	}
	loop.Advance(60_000)
	// The check goes on finding the blocker: once reported, nothing more, and no timer.
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	loop.Advance(60_000)
	if len(object.reports) != 1 || loop.TimerCount() != 0 {
		t.Fatalf("reports %d timers %d, want 1 0", len(object.reports), loop.TimerCount())
	}
}

// Mirrors "starts its clock when the hold gets on a link: a late link is not reported at once".
func TestParkedLongStartsItsClockWhenTheHoldGetsOnALink(t *testing.T) {
	loop, object, long := parkedLongHarness()
	long.waitingOn(nil)
	loop.Advance(5_000)
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	linked := loop.Now()
	loop.Advance(10)
	if len(object.reports) != 0 {
		t.Fatalf("reports 10 ms after the link = %v, want none", object.reports)
	}
	loop.Advance(990)
	if want := []HostError{parkedLongReport("app.notes(S1)", linked)}; !reflect.DeepEqual(object.reports, want) {
		t.Fatalf("reports = %v, want %v", object.reports, want)
	}
}

// Mirrors "is not reported while the path has no link: no remote attempt answers for it": with no remote blocker, nothing
// runs, and the first one found starts the clock.
func TestParkedLongIsNotReportedWhileNoRemoteBlockerIsOnALink(t *testing.T) {
	loop, object, long := parkedLongHarness()
	for range 5 {
		long.waitingOn(nil)
		loop.Advance(1_000)
	}
	if len(object.reports) != 0 || loop.TimerCount() != 0 {
		t.Fatalf("reports %d timers %d, want 0 0", len(object.reports), loop.TimerCount())
	}
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	loop.Advance(5_000)
	if len(object.reports) != 1 || object.reports[0].Kind != ErrParkedLong {
		t.Fatalf("reports = %v, want one parked_long", object.reports)
	}
}

// Mirrors "is not reported for a remote blocker that failed: the parked attempt shows its cause instead": the blocker
// gone, the timer and the clock stop.
func TestParkedLongStopsWhenTheBlockerIsGone(t *testing.T) {
	loop, object, long := parkedLongHarness()
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	loop.Advance(500)
	long.waitingOn(nil)
	loop.Advance(60_000)
	if len(object.reports) != 0 || loop.TimerCount() != 0 {
		t.Fatalf("reports %d timers %d, want 0 0", len(object.reports), loop.TimerCount())
	}
	// A blocker back starts the clock again, from then: only the time a remote attempt answered for the wait counts.
	long.waitingOn(dependencyBlocker("app.notes(S2)"))
	back := loop.Now()
	loop.Advance(999)
	if len(object.reports) != 0 {
		t.Fatalf("reports before the new clock ran out = %v", object.reports)
	}
	loop.Advance(1)
	if want := []HostError{parkedLongReport("app.notes(S2)", back)}; !reflect.DeepEqual(object.reports, want) {
		t.Fatalf("reports = %v, want %v", object.reports, want)
	}
}

// Own test of "The remote blocker it would name now": its one timer is kept while the blocker changes, and the report names
// the blocker of the moment.
func TestParkedLongNamesTheBlockerOfTheMomentWithOneTimer(t *testing.T) {
	loop, object, long := parkedLongHarness()
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	loop.Advance(400)
	long.waitingOn(dependencyBlocker("app.notes(S2)"))
	if loop.TimerCount() != 1 {
		t.Fatalf("timers = %d, want 1", loop.TimerCount())
	}
	loop.Advance(600)
	if want := []HostError{parkedLongReport("app.notes(S2)", 0)}; !reflect.DeepEqual(object.reports, want) {
		t.Fatalf("reports = %v, want %v", object.reports, want)
	}
}

// Own test of the timer's delay: the time left of readyTimeout from `since`, never negative (a timer of 0 runs at once, as
// the loop's nodeDelay gives it 1 ms).
func TestParkedLongDelayIsTheTimeLeft(t *testing.T) {
	loop, object, long := parkedLongHarness()
	object.ready = 0
	long.waitingOn(dependencyBlocker("app.notes(S1)"))
	loop.Advance(1)
	if len(object.reports) != 1 {
		t.Fatalf("reports with readyTimeout 0 = %v, want one", object.reports)
	}
}
