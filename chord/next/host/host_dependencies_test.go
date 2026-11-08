package host

import (
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/sqlite"
)

// pi: packages/chord/src/next/host/dependencies.ts
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
	view, err := host.Hold(background, depView, jsonObject("id", float64(1)), GetOptions{})
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
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	return host, errs, peer, ends
}

// Mirrors "breaks a dependent whose dependency ended (gone): the request cannot come back".
func TestTheDependencyCheckBreaksADependentWhoseDependencyEndedGone(t *testing.T) {
	client, errs, peer, ends := depClient(t, HostOptions{})
	defer closeHosts(t, client)
	view, err := client.Hold(background, depView, jsonObject("id", float64(1)), GetOptions{})
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
	mustLoad(t)(client.Load([]HostFacet{{Name: "@test/view", Facet: facet}}, background))
	view, err := client.Hold(background, depView, jsonObject("id", float64(1)), GetOptions{})
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
	view, err := client.Hold(background, depView, jsonObject("id", float64(1)), GetOptions{})
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
	r.WorkerLoaded(t, "session|S1")
	r.Transported()
	worker := r.Worker("session|S1")
	held, err := worker.Hold(background, b, jsonObject(), GetOptions{})
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

// parkedLongOf is the report `parked_long` makes: the first blocker (`waitingFor`) and its state, loading or failed.
func parkedLongOf(object, waitingFor, state string, since int64) HostError {
	return HostError{Kind: ErrParkedLong, Object: object, WaitingFor: waitingFor, State: state, Since: since}
}

// depMissing is a local type nobody serves: `Missing` of the parked_long cases.
var depMissing = mustContract(declareLocal("app.missing", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

// serveViewOfMissing serves View reading Missing's `n`.
func serveViewOfMissing(h FacetHost, _ FacetSelf) error {
	return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
		return readsN(self, depMissing, jsonObject(), "n"), nil
	}, ServeOptions{})
}

// Mirrors "parked_long (dependency model §7, audit A11) › is reported once after readyTimeout whatever the blocker: a missing local provider; nothing fails".
func TestParkedLongIsReportedOnceAfterReadyTimeoutWhateverTheBlockerAMissingLocalProviderNothingFails(t *testing.T) {
	loop := newScopeLoop()
	ready := time.Second
	host, errs := loaded(t, serveViewOfMissing, loadedOptions{Loop: loop, ReadyTimeout: &ready})
	defer closeHosts(t, host)
	view := mustHold(t, host, depView, jsonObject("id", float64(1)))
	loop.AdvanceAsync(999)
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	loop.AdvanceAsync(1)
	if got, want := errs.All(), []HostError{parkedLongOf("app.view(1)", "app.missing", "failed", 0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	// Its unbound reference was reported too, after 5 s (`unbound_long`, unbound-clock.test.ts); no second parked_long.
	loop.AdvanceAsync(60000)
	var kinds []HostErrorKind
	for _, err := range errs.All() {
		kinds = append(kinds, err.Kind)
	}
	if !reflect.DeepEqual(kinds, []HostErrorKind{ErrParkedLong, ErrUnboundLong}) {
		t.Fatalf("error kinds = %v, want [parked_long unbound_long]", kinds)
	}
	if state := refStateOf(view); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	view.Release()
}

// Mirrors "names the first blocker in declaration order with its state; its clock runs from parking, with no link yet too".
func TestParkedLongNamesTheFirstBlockerInDeclarationOrderWithItsStateItsClockRunsFromParkingWithNoLinkYetToo(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	ready := time.Second
	client := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: errs.push, loop: loop})
	defer closeHosts(t, client)
	mustLoad(t)(client.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			count := self.Get(Notes, jsonObject("session", "S1")).Member("count")
			missing := self.Get(depMissing, jsonObject()).Member("n")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{count, missing}, func(values []any) (delta.JsonValue, error) {
					return values[0].(float64) + values[1].(float64), nil
				}),
			}}}, nil
		}, ServeOptions{})
	})}}, background))
	view := mustHold(t, client, depView, jsonObject("id", float64(1)))
	loop.AdvanceAsync(1000)
	// The remote one comes first (no link holds it yet), though the attempt shows the failed one.
	if got, want := errs.All(), []HostError{parkedLongOf("app.view(1)", "app.notes(S1)", "loading", 0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	if state := refStateOf(view); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	view.Release()
}

// Mirrors "keeps declaration order across registries and references: the parked cause and the report name the first (Astra)".
func TestParkedLongKeepsDeclarationOrderAcrossRegistriesAndReferencesTheParkedCauseAndTheReportNameTheFirstAstra(t *testing.T) {
	missingRegistry, err := registries.DefineRegistry[any, any]("app.missing-registry", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	loop := newScopeLoop()
	ready := time.Second
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.Registry(registries.Registry[any, any](missingRegistry))
			return readsN(self, depMissing, jsonObject(), "n"), nil
		}, ServeOptions{})
	}, loadedOptions{Loop: loop, ReadyTimeout: &ready})
	defer closeHosts(t, host)
	view := mustHold(t, host, depView, jsonObject("id", float64(1)))
	turn(host)
	state := refStateOf(view)
	if state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting ||
		!strings.HasPrefix(state.Cause.Error.Message, "waiting for registry app.missing-registry: ") {
		t.Fatalf("state = %+v (cause %+v), want failed waiting for registry app.missing-registry", state, state.Cause)
	}
	loop.AdvanceAsync(1000)
	want := []HostError{parkedLongOf("app.view(1)", "registry app.missing-registry", "failed", 0)}
	if got := errs.All(); !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	view.Release()
}

// Mirrors "names a reference whose pinned key still loads: the episode's clock includes the load (Sol)". Pi delays
// `ChordObject.prototype.pinKey` with a spy; Go has no such hook, so the pin is delayed where it waits for real: the
// persisted key's cold load, held at the store (as reference-keys' "delayed keyed pin").
func TestParkedLongNamesAReferenceWhosePinnedKeyStillLoadsTheEpisodesClockIncludesTheLoadSol(t *testing.T) {
	keys := mustContract(declareLocal("app.keys", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("notes", contracts.Keyed(kmSchema))},
	}))
	rows := kmDefine("rows", documents.StateOptions{
		Keyed: true, Persist: true,
		CheckpointWhen: func(*delta.JsonObject, []delta.Op, documents.CheckpointInfo) bool { return false },
	})
	gate := newReadGate()
	st := openMemoryStore(t, func(db sqlite.SqliteDatabase) sqlite.SqliteDatabase {
		return &readGateDatabase{SqliteDatabase: db, gate: gate}
	}, nil)
	t.Cleanup(gate.Release)
	var memos atomic.Pointer[documents.Keyed]
	facet := func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(keys, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			notes := self.State(rows).(*documents.Keyed)
			memos.Store(notes)
			return contracts.Implementation{Members: contracts.VariantImplementation{Keyed: kmKeyed(notes)}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(depView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			row, err := self.Get(keys, jsonObject()).Keyed("notes").At("a")
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"n": self.Derive([]any{Expose(row)}, func(values []any) (delta.JsonValue, error) {
					return values[0].(*delta.JsonObject).Value("n"), nil
				}),
			}}}, err
		}, ServeOptions{})
	}
	// The key is stored by an earlier host: the second one finds it cold, and its pin loads it from the store.
	seed, _ := loaded(t, facet, loadedOptions{Store: st})
	mustGet(t, seed, keys, jsonObject())
	kmSet(t, memos.Load(), "a", 4)
	must(t, seed.Close(background))
	loop := newScopeLoop()
	ready := time.Second
	gate.armed.Store(true)
	host, errs := loaded(t, facet, loadedOptions{Loop: loop, ReadyTimeout: &ready, Store: st})
	defer closeHosts(t, host)
	view := mustHold(t, host, depView, jsonObject("id", float64(1)))
	<-gate.entered
	loop.AdvanceAsync(1000)
	if got, want := errs.All(), []HostError{parkedLongOf("app.view(1)", "app.keys", "loading", 0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	gate.Release()
	untilKind(t, view, RefStateLive)
	view.Release()
}

// Mirrors "reports a ring across hosts whose member there waits on a failed dependency (it shows failed here)".
func TestParkedLongReportsARingAcrossHostsWhoseMemberThereWaitsOnAFailedDependencyItShowsFailedHere(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	ready := time.Second
	client := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: errs.push, loop: loop})
	defer closeHosts(t, client)
	peer, ends := rawParentLinks(t, client)
	mustLoad(t)(client.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	view := mustHold(t, client, depView, jsonObject("id", float64(1)))
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	// The provider's object is parked there on a failed dependency (the ring's member back to this host waits too).
	peer.Send(&link.FailedMessage{Hold: hold, Cause: link.WireCause{Kind: "waiting", Error: jsonObject(
		"kind", "chord", "error", jsonObject("code", "unavailable", "message", "waiting for app.c: not served"))}})
	transported(ends[0], ends[1])
	loop.AdvanceAsync(1000)
	if got, want := errs.All(), []HostError{parkedLongOf("app.view(1)", "app.notes(S1)", "failed", 0)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("errors = %+v, want %+v", got, want)
	}
	if state := refStateOf(view); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
		t.Fatalf("state = %+v, want failed waiting", state)
	}
	view.Release()
}

// Mirrors "is reported once per parking episode: again for an attempt that parks after leaving parking".
func TestParkedLongIsReportedOncePerParkingEpisodeAgainForAnAttemptThatParksAfterLeavingParking(t *testing.T) {
	loop := newScopeLoop()
	ready := time.Second
	host, errs := loaded(t, serveViewOfMissing, loadedOptions{Loop: loop, ReadyTimeout: &ready})
	defer closeHosts(t, host)
	view := mustHold(t, host, depView, jsonObject("id", float64(1)))
	advanceSettled(loop, host, 4000)
	var kinds []HostErrorKind
	for _, err := range errs.All() {
		kinds = append(kinds, err.Kind)
	}
	if !reflect.DeepEqual(kinds, []HostErrorKind{ErrParkedLong}) {
		t.Fatalf("error kinds = %v, want [parked_long]", kinds)
	}
	// A reload of its plugin replaces the parked attempt: a new episode, its clock from its own parking.
	_, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(serveViewOfMissing)}}, background, ReloadOptions{})
	must(t, err)
	advanceSettled(loop, host, 999)
	if errs.Len() != 1 {
		t.Fatalf("errors = %d, want 1", errs.Len())
	}
	advanceSettled(loop, host, 1)
	if got, want := errs.All()[1], parkedLongOf("app.view(1)", "app.missing", "failed", 4000); !reflect.DeepEqual(got, want) {
		t.Fatalf("second report = %+v, want %+v", got, want)
	}
	view.Release()
}

// Mirrors "is not reported for an attempt that leaves parking within readyTimeout: its timer stops".
func TestParkedLongIsNotReportedForAnAttemptThatLeavesParkingWithinReadyTimeoutItsTimerStops(t *testing.T) {
	loop := newScopeLoop()
	errs := &hostErrors{}
	ready := time.Second
	client := newHost(t, HostOptions{ReadyTimeout: &ready, OnError: errs.push, loop: loop})
	defer closeHosts(t, client)
	peer, ends := rawParentLinks(t, client)
	mustLoad(t)(client.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	view := mustHold(t, client, depView, jsonObject("id", float64(1)))
	hold := peer.Next("hold").(*link.HoldMessage).Hold
	loop.AdvanceAsync(500)
	bindHold(t, peer, hold, jsonObject("text", "a"))
	transported(ends[0], ends[1])
	loop.AdvanceAsync(5000)
	if state := refStateOf(view); state.Kind != RefStateLive {
		t.Fatalf("state = %+v, want live", state)
	}
	if errs.Len() != 0 {
		t.Fatalf("errors = %v, want none", errs.All())
	}
	view.Release()
}

// Mirrors "a getter of a parked object (fix round 1, Astra 6) › rejects with the last parked cause after its dependency fell back to loading, the newest of several".
func TestAGetterOfAParkedObjectRejectsWithTheLastParkedCauseAfterItsDependencyFellBackToLoading(t *testing.T) {
	host := newHost(t, HostOptions{OnError: func(HostError) {}})
	defer closeHosts(t, host)
	peer, ends := rawParentLinks(t, host)
	mustLoad(t)(host.Load([]HostFacet{{Name: "@test/view", Facet: DefineFacet(serveViewOfNotes)}}, background))
	ready := 80 * time.Millisecond
	pending := make(chan error, 1)
	go func() {
		_, err := host.Get(background, depView, jsonObject("id", float64(1)), GetOptions{ReadyTimeout: &ready})
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
	absent := registries.NewRegistryRecord("app.absent", e.batch, func(registries.RegistryError) {}, nil)
	got := checkDependencies([]dependency{live, {kind: dependencyRegistry, value: absent.Value(1)}})
	if got.kind != checkParked || got.shown.Kind != RefStateFailed || got.shown.Cause.Kind != data.FailureWaiting || !got.firstPass ||
		got.blocker != (blocker{label: "registry app.absent", state: blockerFailed}) || got.loadingOn != nil {
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
