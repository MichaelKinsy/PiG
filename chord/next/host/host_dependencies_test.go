package host

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/test/next/host/dependencies.test.ts. Its cases run hosts (loaded/createHost with defineFacet,
// host.hold, raw parent peers, relayed hosts): "the dependency check" breaks or parks a view object by what its
// dependencies do, "parked_long" counts readyTimeout on a hold that is on a link, and "a getter of a parked object" is
// host.get's. Two own tests of the registry dependencies and `waitingOn` follow the file's cases (no host needed).

var (
	depView = mustContract(declareLocal("app.view", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	// depPing and depPong are local types with empty params and one member, `n`.
	depPing = mustContract(declareLocal("app.ping", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	depPong = mustContract(declareLocal("app.pong", contracts.LocalDeclaration{Params: contracts.Params{}, Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
)

// readsN is `{ n: self.get(Type, params).n }`: a state member `n` following another reference's.
func readsN(self Self, typ *contracts.Contract, params *delta.JsonObject, member string) contracts.Implementation {
	return readsAs(self, typ, params, member, "n")
}

// readsAs is `{ [as]: self.get(Type, params)[member] }`.
func readsAs(self Self, typ *contracts.Contract, params *delta.JsonObject, member, as string) contracts.Implementation {
	n := self.Get(typ, params).Member(member)
	return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
		as: self.Derive([]any{n}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
	}}}
}

// serveViewOfNotes serves View reading Notes `S1`'s `count`.
func serveViewOfNotes(h FacetHost, _ FacetSelf) error {
	return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		return readsN(self, Notes, jsonObject("session", "S1"), "count"), nil
	}, ServeOptions{})
}

// awaitRefState waits until pred holds of ref's state.
func awaitRefState(t testing.TB, what string, ref *Reference, pred func(*RefState) bool) *RefState {
	t.Helper()
	var current *RefState
	scopeEventually(t, what, func() bool {
		current = refStateOf(ref)
		return pred(current)
	})
	return current
}

// wantBroken fails unless state is failed broken with an error of code (and reason when not empty).
func wantBroken(t testing.TB, state *RefState, code data.ChordErrorCode, reason data.ChordErrorReason) {
	t.Helper()
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureBroken || state.Cause.Error.Code != code || state.Cause.Error.Reason != reason {
		t.Fatalf("state = %+v, want failed broken %s/%s", state, code, reason)
	}
}

// Mirrors "the dependency check › breaks a dependent off a cycle with the cycle's error: it would wait on a ring that never heals".
func TestTheDependencyCheckBreaksADependentOffACycleWithTheCyclesError(t *testing.T) {
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		for _, serve := range []func() error{
			func() error {
				return h.Serve(depPing, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					return readsN(self, depPong, jsonObject(), "n"), nil
				}, ServeOptions{})
			},
			func() error {
				return h.Serve(depPong, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					return readsN(self, depPing, jsonObject(), "n"), nil
				}, ServeOptions{})
			},
			func() error {
				return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
					return readsN(self, depPing, jsonObject(), "n"), nil
				}, ServeOptions{})
			},
		} {
			if err := serve(); err != nil {
				return err
			}
		}
		return nil
	}, loadedOptions{})
	defer closeHosts(t, host)
	view, err := host.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	state := awaitRefState(t, "view failed broken", view, func(s *RefState) bool { return s.Kind == RefStateFailed })
	wantBroken(t, state, data.CodeInvalid, data.ReasonCycle)
	if !contains(failures(errs.All()), "app.view(1): broken") {
		t.Fatalf("failures = %v, want app.view(1): broken among them", failures(errs.All()))
	}
	view.Release()
}

// contains is slices.Contains for the file's strings.
func contains(list []string, want string) bool {
	return slices.Contains(list, want)
}

// depClient is a client host with errors collected, a raw parent peer and View (reading Notes S1's count) loaded.
func depClient(t *testing.T, options HostOptions) (Host, *hostErrors, *RawPeer, [2]*link.MemoryLink) {
	t.Helper()
	errs := &hostErrors{}
	options.OnError = errs.push
	host := newHost(t, options)
	peer, ends := rawParentLinks(t, host)
	must(t, host.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	return host, errs, peer, ends
}

// Mirrors "breaks a dependent whose dependency ended (gone): the request cannot come back".
func TestTheDependencyCheckBreaksADependentWhoseDependencyEndedGone(t *testing.T) {
	client, errs, peer, ends := depClient(t, HostOptions{})
	defer closeHosts(t, client)
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	peer.Send(&link.GoneMessage{Hold: hold, Error: revokedWire("revoked")})
	transported(ends[0], ends[1])
	state := awaitRefState(t, "view failed", view, func(s *RefState) bool { return s.Kind == RefStateFailed })
	wantBroken(t, state, data.CodeForbidden, data.ReasonRevoked)
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.view(1): broken"}) {
		t.Fatalf("failures = %v, want [app.view(1): broken]", got)
	}
	view.Release()
}

// Mirrors "goes live once every dependency is: one parked on two, the second binding last".
func TestTheDependencyCheckGoesLiveOnceEveryDependencyIs(t *testing.T) {
	client := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, client)
	peer, ends := rawParentLinks(t, client)
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			a := self.Get(Notes, jsonObject("session", "S1")).Member("count")
			b := self.Get(Notes, jsonObject("session", "S2")).Member("count")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{a, b}, func(values []any) (delta.JsonValue, error) {
					return values[0].(float64) + values[1].(float64), nil
				}),
			}}}, nil
		}, ServeOptions{})
	})
	must(t, client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background))
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	first := peer.Next("hold").(*link.HoldMessage)
	second := peer.Next("hold").(*link.HoldMessage)
	bindHold(t, peer, first.Hold, jsonObject("text", "a"))
	transported(ends[0], ends[1])
	if kind := refStateOf(view).Kind; kind != RefStateLoading {
		t.Fatalf("state = %s, want loading", kind)
	}
	bindHold(t, peer, second.Hold, jsonObject("text", "b"), jsonObject("text", "c"))
	transported(ends[0], ends[1])
	awaitRefState(t, "view live", view, func(s *RefState) bool { return s.Kind == RefStateLive })
	n, err := view.Member("n").Value()
	must(t, err)
	if n != float64(3) {
		t.Fatalf("n = %v, want 3", n)
	}
	view.Release()
}

// Mirrors "breaks a dependent whose dependency a relay's authorize refused: forbidden heals only by retry() (fix round 1, Fable)".
func TestTheDependencyCheckBreaksADependentWhoseDependencyARelaysAuthorizeRefused(t *testing.T) {
	client, errs, peer, ends := depClient(t, HostOptions{})
	defer closeHosts(t, client)
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "broken", Error: jsonObject(
		"kind", "chord", "error", jsonObject("code", "forbidden", "message", "session|S1: not authorized"))}})
	transported(ends[0], ends[1])
	state := awaitRefState(t, "view failed", view, func(s *RefState) bool { return s.Kind == RefStateFailed })
	wantBroken(t, state, data.CodeForbidden, "")
	if state.Cause.Error.Message != "session|S1: not authorized" {
		t.Fatalf("message = %q, want session|S1: not authorized", state.Cause.Error.Message)
	}
	if got := failures(errs.All()); !reflect.DeepEqual(got, []string{"app.view(1): broken"}) {
		t.Fatalf("failures = %v, want [app.view(1): broken]", got)
	}
	view.Release()
}

// Mirrors "settles a waiting ring across hosts after one round: the root cause kept (fix round 1, Opus F1)".
func TestTheDependencyCheckSettlesAWaitingRingAcrossHostsAfterOneRound(t *testing.T) {
	remoteN := func(name string, address func() contracts.Address) *contracts.Contract {
		return mustContract(declareRemote(name, contracts.RemoteDeclaration{
			Version:     1,
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return address(), nil },
			Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
		}))
	}
	a := remoteN("app.a", func() contracts.Address { return Server.Address(nil) })
	b := remoteN("app.b", func() contracts.Address { return Session.Address(jsonObject("id", "S1")) })
	localN := func(name string) *contracts.Contract {
		return mustContract(declareLocal(name, contracts.LocalDeclaration{Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())}}))
	}
	x, kick, front, back := localN("app.x"), localN("app.kick"), localN("app.front"), localN("app.back")
	local := func(h FacetHost, typ *contracts.Contract, read *contracts.Contract) error {
		return h.Serve(typ, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsN(self, read, jsonObject(), "n"), nil
		}, ServeOptions{})
	}
	remoteImpl := func(h FacetHost, typ *contracts.Contract, deps ...*contracts.Contract) error {
		return h.Serve(typ, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			implementation := readsN(self, deps[0], jsonObject(), "n")
			for _, dep := range deps[1:] {
				self.Get(dep, jsonObject())
			}
			implementation.Variant = "v"
			return implementation, nil
		}, ServeOptions{})
	}
	r := relay(t, relayedOptions{
		Trusted: true,
		Server: func(h FacetHost) error {
			// The server's host object starts the worker; its A and the worker's B then start in the same turns, so the
			// activation chains miss the ring (M§7's gap).
			if err := local(h, kick, b); err != nil {
				return err
			}
			return remoteImpl(h, a, b)
		},
		Worker: func(h FacetHost) error {
			if err := local(h, front, a); err != nil {
				return err
			}
			if err := local(h, back, b); err != nil {
				return err
			}
			return remoteImpl(h, b, a, x)
		},
	})
	defer closeHosts(t, r.Tab, r.Server)
	scopeEventually(t, "the server's host object started the worker", func() bool { return r.Worker("session|S1") != nil })
	r.Transported()
	worker := r.Worker("session|S1")
	held, err := worker.Hold(b, jsonObject(), background, GetOptions{})
	must(t, err)
	for range 50 {
		r.Transported()
	}
	watch, err := inSection(worker, func() (chord.NativeWatchHandle[*RefState], error) { return held.State().Watch(background) })
	must(t, err)
	states := statesOf(t, watch)
	for range 50 {
		r.Transported()
	}
	if got := states.All(); len(got) != 0 {
		t.Fatalf("states = %v, want none", got)
	}
	state := refStateOf(held)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting || !strings.HasSuffix(state.Cause.Error.Message, ": app.x is not served here") {
		t.Fatalf("state = %+v, want failed waiting ending in app.x is not served here", state)
	}
	held.Release()
}

// since is the report `parked_long` makes.
func parkedLongOf(object, waitingFor string, since int64) HostError {
	return HostError{Kind: ErrParkedLong, Object: object, WaitingFor: waitingFor, Since: since}
}

// parkedClient is a client host on a fake clock with readyTimeout 1000 ms and View loaded.
func parkedClient(t *testing.T, loop *scopeLoop) (Host, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	ready := time.Second
	host := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: errs.push, loop: loop})
	must(t, host.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	return host, errs
}

// Mirrors "parked_long (dependency model §7) › starts its clock when the hold gets on a link: a late link is not reported at once (fix round 2, Opus F2)".
func TestParkedLongStartsItsClockWhenTheHoldGetsOnALinkALateLinkIsNotReportedAtOnce(t *testing.T) {
	loop := newScopeLoop()
	client, errs := parkedClient(t, loop)
	defer closeHosts(t, client)
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	loop.AdvanceAsync(5000)
	peer, _ := rawParentLinks(t, client)
	peer.Next("hold")
	linkedAt := loop.Now()
	loop.AdvanceAsync(10)
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	loop.AdvanceAsync(990)
	if got, want := errs.All(), []HostError{parkedLongOf("app.view(1)", "app.notes(S1)", linkedAt)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	view.Release()
}

// Mirrors "is not reported while the path has no link: no remote attempt answers for it (fix round 1, Opus F2)".
func TestParkedLongIsNotReportedWhileThePathHasNoLink(t *testing.T) {
	loop := newScopeLoop()
	client, errs := parkedClient(t, loop)
	defer closeHosts(t, client)
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	loop.AdvanceAsync(5000)
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	if kind := refStateOf(view).Kind; kind != RefStateLoading {
		t.Fatalf("state = %s, want loading", kind)
	}
	// Once a link holds it and the remote attempt is only loading, it is.
	peer, _ := rawParentLinks(t, client)
	peer.Next("hold")
	loop.AdvanceAsync(5000)
	var kinds []HostErrorKind
	for _, err := range errs.All() {
		kinds = append(kinds, err.Kind)
	}
	if !reflect.DeepEqual(kinds, []HostErrorKind{ErrParkedLong}) {
		t.Fatalf("error kinds = %v, want [parked_long]", kinds)
	}
	view.Release()
}

// Mirrors "is reported once per parking episode, after readyTimeout on a remote attempt only loading; nothing fails".
func TestParkedLongIsReportedOncePerParkingEpisodeAfterReadyTimeout(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	ready := time.Second
	client := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: errs.push, loop: loop})
	defer closeHosts(t, client)
	peer, _ := rawParentLinks(t, client)
	must(t, client.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	peer.Next("hold")
	loop.AdvanceAsync(999)
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	loop.AdvanceAsync(1)
	if got, want := errs.All(), []HostError{parkedLongOf("app.view(1)", "app.notes(S1)", 0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	loop.AdvanceAsync(60000)
	if errs.Len() != 1 {
		t.Fatalf("errors = %d, want 1", errs.Len())
	}
	if kind := refStateOf(view).Kind; kind != RefStateLoading {
		t.Fatalf("state = %s, want loading", kind)
	}
	view.Release()
}

// Mirrors "is not reported for a remote blocker that failed: the parked attempt shows its cause instead".
func TestParkedLongIsNotReportedForARemoteBlockerThatFailed(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	ready := time.Second
	client := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: errs.push, loop: loop})
	defer closeHosts(t, client)
	peer, ends := rawParentLinks(t, client)
	must(t, client.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	view, err := client.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "waiting", Error: jsonObject(
		"kind", "chord", "error", jsonObject("code", "unavailable", "message", "app.notes is not served here"))}})
	transported(ends[0], ends[1])
	loop.AdvanceAsync(60000)
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	state := awaitRefState(t, "the parked view shows its cause", view, func(s *RefState) bool { return s.Kind == RefStateFailed })
	if state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	if got := loop.TimerCount(); got != 0 {
		t.Fatalf("timers = %d, want 0", got)
	}
	view.Release()
}

// Mirrors "is not reported for a local blocker: its own deadline bounds it".
func TestParkedLongIsNotReportedForALocalBlocker(t *testing.T) {
	slow := mustContract(declareLocal("app.slow", contracts.LocalDeclaration{
		Version: 1,
		Params:  contracts.Params{contracts.Prop("id", contracts.Int())},
		Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	}))
	loop := newScopeLoop()
	ready := time.Second
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(slow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			selfOpen(self, func(ctx context.Context) error { <-ctx.Done(); return nil }, func(NativeCloseReason) {})
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(1))}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return readsN(self, slow, jsonObject("id", float64(1)), "n"), nil
		}, ServeOptions{})
	}, loadedOptions{ReadyTimeout: &ready, Loop: loop})
	defer closeHosts(t, host)
	view, err := host.Hold(depView, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	loop.AdvanceAsync(5000)
	for _, err := range errs.All() {
		if err.Kind == ErrParkedLong {
			t.Fatalf("a parked_long was reported: %+v", err)
		}
	}
	view.Release()
}

// Mirrors "a getter of a parked object (fix round 1, Astra 6) › rejects with the last parked cause after its dependency fell back to loading, the newest of several".
func TestAGetterOfAParkedObjectRejectsWithTheLastParkedCauseAfterItsDependencyFellBackToLoading(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	peer, ends := rawParentLinks(t, host)
	must(t, host.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	ready := 80 * time.Millisecond
	pending := make(chan error, 1)
	go func() {
		_, err := host.Get(depView, jsonObject("id", float64(1)), background, GetOptions{ReadyTimeout: &ready})
		pending <- err
	}()
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	for _, message := range []string{"first absence", "specific provider absence"} {
		peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "waiting", Error: jsonObject(
			"kind", "chord", "error", jsonObject("code", "unavailable", "message", message))}})
		transported(ends[0], ends[1])
	}
	peer.Send(&link.LoadingMessage{Hold: hold})
	transported(ends[0], ends[1])
	wantScopeError(t, <-pending, data.CodeUnavailable, "", "waiting for app.notes(S1): specific provider absence")
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
}

// Own test of `waitingOn`: a parked attempt's failed state, waiting, with no retry time, and a new object each time.
func TestWaitingOnIsAFailedWaitingStateWithoutRetryAt(t *testing.T) {
	err := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "offline"}, nil)
	got := waitingOn("app.notes(S1)", err)
	want := data.WaitingFor("app.notes(S1)", err)
	if got.Kind != RefStateFailed || got.RetryAt != nil || !reflect.DeepEqual(got.Cause, want) {
		t.Fatalf("waitingOn = %+v, want failed waiting %+v", got, want)
	}
}
