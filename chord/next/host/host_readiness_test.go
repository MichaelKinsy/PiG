package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// errNotYet is the error of the native of a request that is down.
var errNotYet = errors.New("not yet")

// pi: packages/chord/test/host/readiness.test.ts
// Ports readiness.test.ts: a restart's readiness (Pi's `Readiness` class), driven alone against a host: when its recorded
// requests are ready, how its failure follows them, presence, release and memory. A readiness runs in a section: the tests enter
// the host to start it and to ask for its promises, as the object does. Pi's `settled(promise)` (a few turns, then whether it
// is settled) is a turn of the host and a look at the promise's channel.

var (
	readinessSlow    = mustContract(declareLocal("app.slow", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	readinessMissing = mustContract(declareLocal("app.missing", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	// readinessTags is as staging records a registry request (it names the registry whatever its types).
	readinessTags = func() registries.Registry[any, any] {
		tags, err := registries.DefineRegistry[any, any]("app.tags", registries.DefineRegistryOptions{Version: 1})
		if err != nil {
			panic(err)
		}
		return tags
	}()
	// readinessSlowRequest has params: an object of no params is a host-lifetime one, opened at the load and never ended for lack
	// of holders.
	readinessSlowRequest = request{kind: requestType, contract: readinessSlow, params: jsonObject("id", 1.0)}
)

// readinessSlowHost is a host serving `Slow`, whose native opens once opened fires and logs its close to closed.
func readinessSlowHost(t testing.TB, opened signal, closed *readinessLog) *chordHost {
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(readinessSlow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(ctx context.Context) (any, error) { opened.await(t, ctx); return nil, nil },
				func(_ any, reason NativeCloseReason, _ context.Context) error {
					closed.push(string(reason.Kind))
					return nil
				})
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	return host.(*chordHost)
}

// readinessLog is a list of strings pushed from any goroutine.
type readinessLog struct {
	mu    sync.Mutex
	items []string
}

func (l *readinessLog) push(item string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, item)
}

func (l *readinessLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.items...)
}

// readinessOf is a readiness of requests on host, as the host's own caller; each failure it shows is pushed to shown ("" when
// none).
func readinessOf(host *chordHost, requests []request, shown *readinessLog) *readiness {
	var made *readiness
	host.enter(func() {
		made = newReadiness(host, caller{principal: host.principal, incarnation: host.incarnation}, requests, func(cause *data.FailureCause) {
			if cause == nil {
				shown.push("")
			} else {
				shown.push(cause.Error.Message)
			}
		}, background)
	})
	return made
}

// readyBy is `readiness.ready(Date.now() + ms)`.
func readyBy(host *chordHost, r *readiness, ms int64) *keyedPromise[struct{}] {
	var promise *keyedPromise[struct{}]
	host.enter(func() { promise = r.ready(host.loop().Now() + ms) })
	return promise
}

// readinessStart is `readiness.start()`.
func readinessStart(host *chordHost, r *readiness) { host.enter(r.start) }

// readinessRelease is `readiness.release()`.
func readinessRelease(host *chordHost, r *readiness) { host.enter(r.release) }

// readinessCause is `readiness.cause`.
func readinessCause(host *chordHost, r *readiness) *data.FailureCause {
	var cause *data.FailureCause
	host.enter(func() { cause = r.cause() })
	return cause
}

// readinessSettled is whether promise settled after a few turns.
func readinessSettled(host *chordHost, promise *keyedPromise[struct{}]) bool {
	for range 5 {
		turn(host)
	}
	select {
	case <-promise.done:
		return true
	default:
		return false
	}
}

func TestReadinessWaitsForARequestStillLoadingAndResolvesOnceItIsLive(t *testing.T) {
	opened := newSignal()
	t.Cleanup(opened.fire)
	host := readinessSlowHost(t, opened, nil)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{readinessSlowRequest}, nil)
	ready := readyBy(host, r, 10_000)
	readinessStart(host, r)
	if readinessSettled(host, ready) {
		t.Fatalf("ready resolved while its request still loads")
	}
	opened.fire()
	receive(t, ready.done)
	if cause := readinessCause(host, r); cause != nil {
		t.Fatalf("cause = %+v, want none", cause)
	}
	readinessRelease(host, r)
}

func TestReadinessWaitsForNothingBeforeItStartedTheRequestsBindOnlyOnceEveryFateStarted(t *testing.T) {
	opened := newSignal()
	opened.fire()
	host := readinessSlowHost(t, opened, nil)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{readinessSlowRequest}, nil)
	ready := readyBy(host, r, 10_000)
	if readinessSettled(host, ready) {
		t.Fatalf("ready resolved before the readiness started")
	}
	readinessStart(host, r)
	receive(t, ready.done)
	readinessRelease(host, r)
}

func TestReadinessResolvesAtItsBoundWhenARequestNeverGetsReady(t *testing.T) {
	never := newSignal()
	t.Cleanup(never.fire)
	host := readinessSlowHost(t, never, nil)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{readinessSlowRequest}, nil)
	readinessStart(host, r)
	started := time.Now()
	receive(t, readyBy(host, r, 50).done)
	if elapsed := time.Since(started); elapsed < 40*time.Millisecond {
		t.Fatalf("resolved after %v, want at its bound (50 ms)", elapsed)
	}
	readinessRelease(host, r)
}

func TestReadinessCountsARequestFailedOnThisHostAsReadyTheReloadReportsItAsWhatTheSuccessorWaitsFor(t *testing.T) {
	opened := newSignal()
	opened.fire()
	host := readinessSlowHost(t, opened, nil)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{{kind: requestType, contract: readinessMissing, params: jsonObject()}}, nil)
	readinessStart(host, r)
	receive(t, readyBy(host, r, 10_000).done)
	cause := readinessCause(host, r)
	if cause == nil || cause.Kind != data.FailureWaiting || cause.Error.Code != data.CodeUnavailable ||
		cause.Error.Message != "waiting for app.missing: app.missing is not served here" {
		t.Fatalf("cause = %+v, want waiting unavailable \"waiting for app.missing: app.missing is not served here\"", cause)
	}
	readinessRelease(host, r)
}

func TestReadinessWaitsUpToItsBoundForARequestAnotherHostRefusesForItsVersionThatHostsReloadHealsIt(t *testing.T) {
	shown := mustContract(declareRemote("app.shown", contracts.RemoteDeclaration{
		Version: 1, HostAddress: exServerAddress,
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{rhProp("text", contracts.Str())}})},
	}))
	shownV2 := mustContract(contracts.Evolve(shown, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.AddState("main", "n", contracts.Int())
	}))
	serve := func(h FacetHost, raised bool) error {
		if raised {
			return h.Serve(shownV2, func(_ *delta.JsonObject, _ data.Principal, _ Self) (contracts.Implementation, error) {
				return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"text": constant("v2"), "n": constant(2.0)},
				}}, nil
			}, ServeOptions{})
		}
		return h.Serve(shown, func(_ *delta.JsonObject, _ data.Principal, _ Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"text": constant("v1")},
			}}, nil
		}, ServeOptions{})
	}
	s := linked(t, func(h FacetHost) error { return serve(h, false) }, linkedOptions{})
	defer closeHosts(t, s.Client, s.Server)
	client := s.Client.(*chordHost)
	r := readinessOf(client, []request{{kind: requestType, contract: shownV2, params: jsonObject()}}, nil)
	readinessStart(client, r)
	ready := readyBy(client, r, 10_000)
	settle := func() { transported(s.Links[0], s.Links[1]); deliveredOn(s.Server, s.Client) }
	settle()
	if readinessSettled(client, ready) {
		t.Fatalf("ready resolved while the other host still refuses its version")
	}
	if cause := readinessCause(client, r); cause != nil {
		t.Fatalf("cause = %+v, want none: a version skew heals by itself", cause)
	}
	reloadMust(t)(s.Server.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serve(h, true) })}}, background, ReloadOptions{}))
	scopeEventually(t, "the readiness is ready", func() bool {
		settle()
		select {
		case <-ready.done:
			return true
		default:
			return false
		}
	})
	readinessRelease(client, r)
}

func TestReadinessDoesNotWaitForRegistryRequestsTheOldAttemptsContributionsKeepThemUntilItsSeal(t *testing.T) {
	never := newSignal()
	t.Cleanup(never.fire)
	host := readinessSlowHost(t, never, nil)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{{kind: requestRegistry, registry: readinessTags}}, nil)
	readinessStart(host, r)
	receive(t, readyBy(host, r, 10_000).done)
	if cause := readinessCause(host, r); cause != nil {
		t.Fatalf("cause = %+v, want none", cause)
	}
	readinessRelease(host, r)
}

func TestReadinessShowsARequestsFailureAndNoneOnceTheRequestRecovered(t *testing.T) {
	flaky := mustContract(declareLocal("app.flaky", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	var failing sync.Mutex
	down := true
	isDown := func() bool { failing.Lock(); defer failing.Unlock(); return down }
	hostAny, _ := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(flaky, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				if isDown() {
					return nil, errNotYet
				}
				return nil, nil
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)}}}, nil
		}, ServeOptions{})
	}, loadedOptions{})
	host := hostAny.(*chordHost)
	defer closeHosts(t, host)
	held := mustHold(t, host, flaky, jsonObject("id", 1.0))
	untilKind(t, held, RefStateFailed)
	shown := &readinessLog{}
	r := readinessOf(host, []request{{kind: requestType, contract: flaky, params: jsonObject("id", 1.0)}}, shown)
	readinessStart(host, r)
	if got := shown.all(); len(got) != 1 || !strings.Contains(got[0], "not yet") {
		t.Fatalf("shown = %q, want one failure naming \"not yet\"", got)
	}
	failing.Lock()
	down = false
	failing.Unlock()
	held.Retry()
	scopeEventually(t, "the failure is shown no more", func() bool {
		got := shown.all()
		return len(got) >= 2 && got[len(got)-1] == ""
	})
	if cause := readinessCause(host, r); cause != nil {
		t.Fatalf("cause = %+v, want none", cause)
	}
	readinessRelease(host, r)
	held.Release()
}

func TestReadinessKeepsItsBindingsOutOfPresenceListsTheyAreNoConsumersOfTheirOwn(t *testing.T) {
	opened := newSignal()
	opened.fire()
	host := readinessSlowHost(t, opened, nil)
	defer closeHosts(t, host)
	list := func() int { return len(presenceList(t, host, readinessSlow)) }
	r := readinessOf(host, []request{readinessSlowRequest}, nil)
	readinessStart(host, r)
	receive(t, readyBy(host, r, 10_000).done)
	if got := list(); got != 0 {
		t.Fatalf("the presence list has %d entries, want none: readiness bindings are no consumers of their own", got)
	}
	held := mustHold(t, host, readinessSlow, jsonObject("id", 1.0))
	turn(host)
	if got := list(); got != 1 {
		t.Fatalf("the presence list has %d entries, want 1", got)
	}
	readinessRelease(host, r)
	held.Release()
}

func TestReadinessReleaseLetsGoOfItsBindingsAnObjectHeldOnlyByThemEnds(t *testing.T) {
	opened := newSignal()
	opened.fire()
	closed := &readinessLog{}
	host := readinessSlowHost(t, opened, closed)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{readinessSlowRequest}, nil)
	readinessStart(host, r)
	receive(t, readyBy(host, r, 10_000).done)
	if got := closed.all(); len(got) != 0 {
		t.Fatalf("closes = %q, want none yet", got)
	}
	readinessRelease(host, r)
	scopeEventually(t, "the object ends", func() bool { return len(closed.all()) == 1 })
	if got := closed.all(); got[0] != "end" {
		t.Fatalf("closes = %q, want [end]", got)
	}
}

func TestReadinessReleaseEndsAWaitStillRunningReadyResolvesAtOnce(t *testing.T) {
	never := newSignal()
	t.Cleanup(never.fire)
	host := readinessSlowHost(t, never, nil)
	defer closeHosts(t, host)
	r := readinessOf(host, []request{readinessSlowRequest}, nil)
	readinessStart(host, r)
	ready := readyBy(host, r, 10_000)
	readinessRelease(host, r)
	if !readinessSettled(host, ready) {
		t.Fatalf("ready did not resolve after the release")
	}
}
