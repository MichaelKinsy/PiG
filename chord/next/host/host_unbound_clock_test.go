package host

import (
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// pi: packages/chord/src/host/unbound-clock.ts
// Ports packages/chord/test/host/unbound-clock.test.ts: the clock, and the requests that wait unbound (decision "Mistakes
// that wait are reported at once" (c)).

// clockRequest is a request of the clock: a pointer of its own (not zero-sized), as upstream's `{}`.
type clockRequest struct{ n int }

// unboundReports collects what a clock reports.
type unboundReports struct{ list []unboundLong }

func (r *unboundReports) push(report unboundLong) { r.list = append(r.list, report) }

func (r *unboundReports) names() []string {
	var names []string
	for _, report := range r.list {
		names = append(names, report.waitingFor)
	}
	return names
}

// Mirrors "the clock › reports each wait once, 5 s after it started, in the order they started; a stopped one never".
func TestUnboundClockReportsEachWaitOnce5SAfterItStartedInTheOrderTheyStartedAStoppedOneNever(t *testing.T) {
	loop := newFakeLoop()
	reports := &unboundReports{}
	clock := newUnboundClock(loop, reports.push)
	failure := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "x"}, nil)
	a, b, c := &clockRequest{1}, &clockRequest{2}, &clockRequest{3}
	clock.Start(a, "app.a", failure)
	loop.AdvanceAsync(2_000)
	clock.Start(b, "app.b", failure)
	clock.Start(c, "app.c", failure)
	clock.Stop(c)
	loop.AdvanceAsync(2_999)
	if len(reports.list) != 0 {
		t.Fatalf("reports before 5 s = %v, want none", reports.list)
	}
	loop.AdvanceAsync(1)
	if len(reports.list) != 1 || reports.list[0] != (unboundLong{waitingFor: "app.a", err: failure, since: 0}) {
		t.Fatalf("reports at 5 s = %+v, want app.a since 0", reports.list)
	}
	loop.AdvanceAsync(2_000)
	if got := reports.names(); len(got) != 2 || got[0] != "app.a" || got[1] != "app.b" {
		t.Fatalf("reports = %v, want [app.a app.b]", got)
	}
	// Reported waits leave the clock: no timer runs, a start again is a new wait.
	if count := loop.TimerCount(); count != 0 {
		t.Fatalf("timers = %d, want 0", count)
	}
}

// Mirrors "the clock › names the latest error of a wait; a wait started twice keeps its start".
func TestUnboundClockNamesTheLatestErrorOfAWaitAWaitStartedTwiceKeepsItsStart(t *testing.T) {
	loop := newFakeLoop()
	reports := &unboundReports{}
	clock := newUnboundClock(loop, reports.push)
	failure := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "x"}, nil)
	later := data.NewChordError(data.ChordErrorInit{Code: data.CodeContractMismatch, Message: "y"}, nil)
	request := &clockRequest{}
	clock.Start(request, "registry app.r", failure)
	loop.AdvanceAsync(1_000)
	clock.Start(request, "registry app.r", failure)
	clock.Failed(request, later)
	loop.AdvanceAsync(4_000)
	if len(reports.list) != 1 || reports.list[0] != (unboundLong{waitingFor: "registry app.r", err: later, since: 0}) {
		t.Fatalf("reports = %+v, want registry app.r with the later error since 0", reports.list)
	}
}

// Mirrors "the clock › keeps no request once it stopped or the clock closed (GC)".
func TestUnboundClockKeepsNoRequestOnceItStoppedOrTheClockClosedGC(t *testing.T) {
	clock := newUnboundClock(newFakeLoop(), func(unboundLong) {})
	failure := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "x"}, nil)
	weaks := func() []weak.Pointer[clockRequest] {
		a, b := &clockRequest{1}, &clockRequest{2}
		clock.Start(a, "app.a", failure)
		clock.Start(b, "app.b", failure)
		clock.Stop(a)
		clock.Close()
		return []weak.Pointer[clockRequest]{weak.Make(a), weak.Make(b)}
	}()
	nexttest.ExpectCollected(t, weaks...)
	if clock == nil {
		t.Fatal("unreachable")
	}
}

// sameUnboundLong checks a reported unbound_long.
func sameUnboundLong(t *testing.T, got HostError, waitingFor string, since int64, code data.ChordErrorCode, message, reason string) {
	t.Helper()
	chordErr, ok := data.IsChordError(got.Error)
	if got.Kind != ErrUnboundLong || got.WaitingFor != waitingFor || got.Since != since || !ok || chordErr.Code != code ||
		(message != "" && chordErr.Message != message) || (reason != "" && string(chordErr.Reason) != reason) {
		t.Fatalf("report = %+v (error %v), want unbound_long %q since %d %s %q %q", got, got.Error, waitingFor, since, code, message, reason)
	}
}

func noServing(FacetHost, FacetSelf) error { return nil }

// Mirrors "requests waiting unbound › reports a host.hold and a host.get of a type nobody serves once after 5 s; the get waits until its readyTimeout".
func TestRequestsWaitingUnboundReportsAHostHoldAndAHostGetOfATypeNobodyServesOnceAfter5STheGetWaitsUntilItsReadyTimeout(t *testing.T) {
	loop := newScopeLoop()
	host, errs := loaded(t, noServing, loadedOptions{Loop: loop})
	defer closeHosts(t, host)
	held := mustHold(t, host, unboundMissing, jsonObject("id", float64(1)))
	got := asyncGet(host, unboundMissing, jsonObject("id", float64(2)), background, GetOptions{ReadyTimeout: new(20 * time.Second)})
	scopeEventually(t, "the get is waiting", func() bool { return len(indexedBindings(host)) == 2 })
	advanceHost(loop, host, 4_999)
	if errs.Len() != 0 {
		t.Fatalf("errors before 5 s = %v", errs.All())
	}
	advanceHost(loop, host, 1)
	if errs.Len() != 2 {
		t.Fatalf("errors at 5 s = %v, want 2", errs.All())
	}
	for _, each := range errs.All() {
		sameUnboundLong(t, each, "app.missing", 0, data.CodeUnavailable, "app.missing is not served here", "")
	}
	advanceHost(loop, host, 14_999)
	if errs.Len() != 2 {
		t.Fatalf("errors before the readyTimeout = %v, want 2", errs.All())
	}
	advanceHost(loop, host, 1)
	result := <-got
	if chordErr, ok := data.IsChordError(result.err); !ok || chordErr.Code != data.CodeUnavailable || chordErr.Message != "app.missing is not served here" {
		t.Fatalf("get = %v, want unavailable app.missing is not served here", result.err)
	}
	if state := refStateOf(held); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("held state = %+v, want failed waiting", state)
	}
	held.Release()
}

// Mirrors "requests waiting unbound › reports nothing for a request that binds or ends within 5 s".
func TestRequestsWaitingUnboundReportsNothingForARequestThatBindsOrEndsWithin5S(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: errs.push, loop: loop})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load(nil, background))
	released := mustHold(t, host, unboundMissing, jsonObject("id", float64(1)))
	bound := mustHold(t, host, unboundMissing, jsonObject("id", float64(2)))
	advanceHost(loop, host, 1_000)
	released.Release()
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(unboundMissing, func(params *delta.JsonObject, _ Self) (contracts.Implementation, error) {
			return impl("n", constant(float64(1))), nil
		}, ServeOptions{})
	})
	_, err := host.Reload([]HostFacet{{Name: "@test/missing", Facet: facet}}, background, ReloadOptions{})
	must(t, err)
	turn(host)
	if state := refStateOf(bound); state.Kind != RefStateLive {
		t.Fatalf("bound state = %+v, want live", state)
	}
	advanceHost(loop, host, 60_000)
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	bound.Release()
}

// Mirrors "requests waiting unbound › reports a hold arriving over a link and refused for a type not served, on the host that refused it".
func TestRequestsWaitingUnboundReportsAHoldArrivingOverALinkAndRefusedForATypeNotServedOnTheHostThatRefusedIt(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	address := serverAddress()
	host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), OnError: errs.push, loop: loop})
	defer closeHosts(t, host)
	a, b := link.MemoryLinkPair()
	must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	params, err := link.JsonPart(jsonObject())
	must(t, err)
	// Before the publication it waits for it without a report; the clock starts once it is refused.
	peer.Send(&link.HoldMessage{
		Hold: 0, Type: "app.remote-missing", Version: 1, Fingerprint: "0123456789abcdef", Params: params, Address: serverWireAddress,
	})
	transported(a, b)
	advanceHost(loop, host, 10_000)
	if errs.Len() != 0 {
		t.Fatalf("errors before the load = %v", errs.All())
	}
	mustLoad(t)(host.Load(nil, background))
	peer.Next("failed")
	advanceHost(loop, host, 5_000)
	if errs.Len() != 1 {
		t.Fatalf("errors = %v, want 1", errs.All())
	}
	sameUnboundLong(t, errs.All()[0], "app.remote-missing", 10_000, data.CodeUnavailable, "app.remote-missing is not served here", "")
}

// Mirrors "requests waiting unbound › reports a registry nobody serves once per value waited for: a parked reader and host.registry".
func TestRequestsWaitingUnboundReportsARegistryNobodyServesOncePerValueWaitedForAParkedReaderAndHostRegistry(t *testing.T) {
	absent, err := registries.DefineRegistry[any, any]("app.absent", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	reader := mustContract(declareLocal("app.reader", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	loop := newScopeLoop()
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(reader, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			value := self.Registry(absent)
			return impl("n", self.Derive([]any{value}, func(values []any) (delta.JsonValue, error) { return float64(0), nil })), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop})
	defer closeHosts(t, host)
	held := mustHold(t, host, reader, jsonObject("id", float64(1)))
	h := host.(*chordHost)
	waits := func() (count int) {
		h.enter(func() { count = len(h.clock.by) })
		return count
	}
	scopeEventually(t, "the parked reader's wait is on the clock", func() bool { return waits() == 1 })
	armed := loop.TimerCount()
	waiting := make(chan error, 1)
	go func() {
		_, err := host.Registry(background, absent, GetOptions{ReadyTimeout: new(30 * time.Second)})
		waiting <- err
	}()
	// host.registry's deadline timer is set in the section that made its wait.
	scopeEventually(t, "host.registry waits", func() bool { return loop.TimerCount() > armed })
	advanceHost(loop, host, 5_000)
	// The parked reader and host.registry wait for one value: one wait, one report.
	if errs.Len() != 1 {
		t.Fatalf("errors at 5 s = %v, want 1", errs.All())
	}
	sameUnboundLong(t, errs.All()[0], "registry app.absent", 0, data.CodeUnavailable, "", "undeclared")
	advanceHost(loop, host, 25_000)
	err = <-waiting
	if chordErr, ok := data.IsChordError(err); !ok || string(chordErr.Reason) != "undeclared" {
		t.Fatalf("host.registry = %v, want unavailable/undeclared", err)
	}
	if errs.Len() != 1 {
		t.Fatalf("errors = %v, want 1", errs.All())
	}
	held.Release()
}

// Mirrors "requests waiting unbound › arm one timer for the host whatever the number of requests waiting (cost guard: no timer per request)".
func TestRequestsWaitingUnboundArmOneTimerForTheHostWhateverTheNumberOfRequestsWaiting(t *testing.T) {
	loop := newScopeLoop()
	host, errs := loaded(t, noServing, loadedOptions{Loop: loop})
	defer closeHosts(t, host)
	before := loop.TimerCount()
	var held []*Reference
	for id := range 100 {
		held = append(held, mustHold(t, host, unboundMissing, jsonObject("id", float64(id))))
	}
	turn(host)
	if got := loop.TimerCount() - before; got != 1 {
		t.Fatalf("timers armed by 100 requests = %d, want 1", got)
	}
	advanceHost(loop, host, 5_000)
	if errs.Len() != 100 {
		t.Fatalf("errors = %d, want 100", errs.Len())
	}
	if got := loop.TimerCount(); got != before {
		t.Fatalf("timers after the reports = %d, want %d", got, before)
	}
	for _, each := range held {
		each.Release()
	}
}
