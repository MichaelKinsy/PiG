package host

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports the final-state cases of packages/chord/test/remote/hold.test.ts that slice 7b added: holds across a reload
// (§6), across more than one reload, after a live failure, and across a reload whose mapping fails.
//
// Go forms: `await transported()` is transported over the links of the hosts, `await delivered()` is deliveredOn, a
// `WeakRef` is a weak pointer collected with expectCollected, `peer.received.length = 0` is rhForget.

// rhShown is `Shown`: a remote object on the server: a label, a counter and `bump`.
var rhShown = mustContract(contracts.Remote("app.shown", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State:   []contracts.Property{rhProp("label", contracts.Str()), rhProp("n", contracts.Int())},
		Methods: []contracts.MethodEntry{contracts.Meth("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
	})},
}))

// rhShownV2 is `Shown` v2: its label became an object; v1 consumers map back through `accepts`.
var rhShownV2 = mustContract(contracts.Evolve(rhShown, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("v", "label", contracts.Obj(contracts.Prop("text", contracts.Str())))
}))

// rhShownMembers are the members of a `Shown` object labelled label (any JSON value), `bump` adding 1 to its counter.
func rhShownMembers(self Self, label any) contracts.VariantImplementation {
	doc := stateOf(self, rhCounter)
	return contracts.VariantImplementation{
		State: map[string]chord.Watchable[delta.JsonValue]{
			"label": rhConst(label),
			"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
				return values[0].(*delta.JsonObject).Value("n"), nil
			}),
		},
		Methods: map[string]contracts.MethodFunction{
			"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				err := doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
				if err != nil {
					return nil, err
				}
				return doc.Value().Value("n"), nil
			},
		},
	}
}

// rhServeShown serves `Shown` labelled label (with a native when native).
func rhServeShown(h FacetHost, label string, native bool) error {
	return h.Serve(rhShown, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		if native {
			self.Open(func(context.Context) (any, error) { return label, nil },
				func(any, NativeCloseReason, context.Context) error { return nil })
		}
		return contracts.Implementation{Variant: "v", Members: rhShownMembers(self, label)}, nil
	}, ServeOptions{})
}

// rhLabelText maps v2's label back to v1's: its text.
func rhLabelText(current delta.JsonValue) (delta.JsonValue, error) {
	return current.(*delta.JsonObject).Value("text"), nil
}

// rhAcceptsLabel is the accepts table of a variant "v" whose member "label" maps through mapping (version 1).
func rhAcceptsLabel(mapping contracts.Mapping) contracts.AcceptsTable {
	return contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
		VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{{Name: "v", Value: []contracts.Entry[contracts.StateMapping]{
			{Name: "label", Value: contracts.StateMapping{Func: mapping}},
		}}},
	}}}
}

// rhServeShownV2 serves `ShownV2` labelled text; with accept, v1 consumers see label as its text.
func rhServeShownV2(h FacetHost, text string, accept bool) error {
	options := ServeOptions{}
	if accept {
		options.Accepts = rhAcceptsLabel(rhLabelText)
	}
	return h.Serve(rhShownV2, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		return contracts.Implementation{Variant: "v", Members: rhShownMembers(self, jsonObject("text", text))}, nil
	}, options)
}

// rhReload reloads the server's plugin `@test/app` (as `linked` names it) with setup.
func rhReload(t testing.TB, host Host, setup func(FacetHost) error) {
	t.Helper()
	_, err := host.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}},
		background, ReloadOptions{})
	must(t, err)
}

// rhSet is `writable.set(value)` of a test: a section of host's loop, as Pi's synchronous call from the event loop.
func rhSet(host Host, writable state.Writable[any], value any) error {
	var err error
	host.(*chordHost).enter(func() { err = writable.Set(value) })
	return err
}

// rhForget is `peer.received.length = 0`.
func rhForget(peer *RawPeer) {
	peer.mu.Lock()
	peer.received = nil
	peer.mu.Unlock()
}

// rhRawServer is a server (a fresh host serving setup as `@test/app`) and a raw peer on a link it accepted as owner.
func rhRawServer(t testing.TB, setup func(FacetHost) error, options rawPeerOptions) (Host, *RawPeer) {
	t.Helper()
	server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}}, background))
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, options)
	peer.Next("hello")
	return server, peer
}

// rhBumpCall calls `bump` (by the variant's wire index) on hold 0 as call number call.
func rhBumpCall(peer *RawPeer, call, method int) {
	peer.Send(&link.CallMessage{Call: call, Hold: 0, Method: method})
}

// rhResultNumber is the number a result message carries.
func rhResultNumber(t testing.TB, message link.Message) any {
	t.Helper()
	return partValue(t, message.(*link.ResultMessage).Value)
}

func rhFailedState(t testing.TB, ref *Reference, code data.ChordErrorCode) {
	t.Helper()
	got := rhState(ref)
	if got.Kind != RefStateFailed || got.Cause.Kind != data.FailureWaiting || got.Cause.Error.Code != code {
		t.Fatalf("state = %+v, want failed waiting %s", got, code)
	}
}

// rhLinks is `await transported()` and the frames queued by it (`await delivered()`) over the linked hosts.
func rhLinks(t testing.TB, l *linkedHosts) {
	// The teardowns the hosts still run (an ended object's drain and release) have finished when Pi's event loop returns
	// from `transported()`.
	for _, host := range []Host{l.Server, l.Client} {
		untilEnded(t, host)
	}
	transported(l.Links[0], l.Links[1])
	deliveredOn(l.Server, l.Client)
}

func TestHoldsAcrossAReload(t *testing.T) {
	// hold.test.ts: holds across a reload (slice 7b §6) › a restarted object's holds see loading, then a fresh bound
	t.Run("a restarted object's holds see loading, then a fresh bound", func(t *testing.T) {
		s := linked(t, func(h FacetHost) error { return rhServeShown(h, "v1", true) }, linkedOptions{})
		shown := refGet(t, s.Client, rhShown, jsonObject())
		watch, err := refStateWatch(shown, background)
		must(t, err)
		states := statesOf(t, watch)
		label := nexttest.ChangesOf(rhWatch(t, shown.Member("label")))
		rhReload(t, s.Server, func(h FacetHost) error { return rhServeShown(h, "v2", true) })
		// The new attempt activates in a task of the server's loop (Pi's event loop has run it when `transported()` returns).
		rhWaitFor(t, func() bool { return len(states.All()) >= 2 })
		rhLinks(t, s)
		rhEqual(t, "states", states.All(), []string{"loading", "live"})
		rhEqual(t, "label", nexttest.ValuesOf(label.Items()), []any{"v2"})
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › a version the new code no longer accepts unbinds its holds (version); a later reload accepting it binds them
	t.Run("a version the new code no longer accepts unbinds its holds (version); a later reload accepting it binds them", func(t *testing.T) {
		s := linked(t, func(h FacetHost) error { return rhServeShown(h, "v1", false) }, linkedOptions{})
		shown := refGet(t, s.Client, rhShown, jsonObject())
		rhReload(t, s.Server, func(h FacetHost) error { return rhServeShownV2(h, "v2", false) })
		rhLinks(t, s)
		rhFailedState(t, shown, data.CodeContractMismatch)
		_, err := shown.Call("bump", nil, background)
		if _, ok := data.IsChordErrorCode(err, data.CodeContractMismatch); !ok {
			t.Fatalf("bump error = %v, want contract_mismatch", err)
		}
		rhReload(t, s.Server, func(h FacetHost) error { return rhServeShownV2(h, "v3", true) })
		rhWaitFor(t, func() bool { return rhRefState(shown) == RefStateLive })
		rhLinks(t, s)
		if kind := rhRefState(shown); kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		rhEqual(t, "label", memberValue(t, shown, "label"), "v3")
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › a removed type unbinds its holds (not_served); served again, they bind and a derive over them heals
	t.Run("a removed type unbinds its holds (not_served); served again, they bind and a derive over them heals", func(t *testing.T) {
		panel := mustContract(contracts.Local("app.panel", contracts.LocalDeclaration{
			Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())},
		}))
		s := linked(t, func(h FacetHost) error { return rhServeShown(h, "v1", false) }, linkedOptions{})
		_, err := s.Client.Reload([]HostFacet{{Name: "@test/panel", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return h.Serve(panel, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
				shown := self.Get(rhShown, jsonObject())
				return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
					"text": self.Derive([]any{shown.Member("label")}, func(values []any) (delta.JsonValue, error) {
						return fmt.Sprintf("panel %v", values[0]), nil
					}),
				}}}, nil
			}, ServeOptions{})
		})}}, background, ReloadOptions{})
		must(t, err)
		panelRef := refGet(t, s.Client, panel, jsonObject())
		shown := rhHoldRef(t, s.Client, rhShown, jsonObject())
		_, err = s.Server.Reload(nil, background, ReloadOptions{})
		must(t, err)
		rhLinks(t, s)
		rhFailedState(t, shown, data.CodeUnavailable)
		// The dependent stays live (R18) with its last value.
		if kind := rhRefState(panelRef); kind != RefStateLive {
			t.Fatalf("panel state = %v, want live", kind)
		}
		rhEqual(t, "panel", memberValue(t, panelRef, "text"), "panel v1")
		rhReload(t, s.Server, func(h FacetHost) error { return rhServeShown(h, "v2", false) })
		rhWaitFor(t, func() bool { return rhRefState(shown) == RefStateLive })
		rhLinks(t, s)
		if kind := rhRefState(shown); kind != RefStateLive {
			t.Fatalf("shown state = %v, want live", kind)
		}
		rhEqual(t, "panel", memberValue(t, panelRef, "text"), "panel v2")
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › a hold unbound while the window is full drops its pending ops; acks of messages sent stay counted
	t.Run("a hold unbound while the window is full drops its pending ops; acks of messages sent stay counted", func(t *testing.T) {
		server, peer := rhRawServer(t, func(h FacetHost) error { return rhServeShown(h, "v1", false) }, rawPeerOptions{NoAck: true})
		rhHold(peer, 0, rhShown)
		peer.Next("bound")
		// Commits from the server itself as the hold's principal: an answer over the link would send the waiting state.
		for range 3 {
			_, err := server.Root(func(ctx Ctx) (any, error) {
				object, err := ctx.Get(rhShown, jsonObject(), GetOptions{})
				if err != nil {
					return nil, err
				}
				return object.Call("bump", nil, ctx)
			}, background, RootOptions{Principal: ownerPrincipal()})
			must(t, err)
		}
		// One state in flight with the bound: the third commit's ops wait in their pending queue.
		peer.Next("state")
		_, err := server.Reload(nil, background, ReloadOptions{})
		must(t, err)
		rhSync(t, peer)
		if failed := peer.Next("failed").(*link.FailedMessage); failed.Cause.Kind != "waiting" {
			t.Fatalf("cause = %+v, want waiting", failed.Cause)
		}
		peer.Ack(link.Optional[int]{})
		peer.Ack(link.Optional[int]{})
		rhSync(t, peer)
		rhKindsAre(t, peer)
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › a restart sends each bound hold loading, then one fresh bound with the new instance's values
	t.Run("a restart sends each bound hold loading, then one fresh bound with the new instance's values", func(t *testing.T) {
		server, peer := rhRawServer(t, func(h FacetHost) error { return rhServeShown(h, "v1", false) }, rawPeerOptions{})
		shown := wireNames(t, rhShown, "v")
		rhHold(peer, 0, rhShown)
		rhHold(peer, 1, rhShown)
		peer.Next("bound")
		rhSync(t, peer)
		rhForget(peer)
		rhReload(t, server, func(h FacetHost) error { return rhServeShown(h, "v2", false) })
		// The new attempt goes live in a task of the server's loop once the old one ended.
		rhWaitFor(t, func() bool { return slices.Contains(peer.Kinds(), "bound") })
		rhSync(t, peer)
		var entries []link.BoundEntry
		for _, message := range peer.Received() {
			switch message.Kind() {
			case "loading":
			case "bound":
				entries = append(entries, message.(*link.BoundMessage).Holds...)
			default:
				t.Fatalf("received %s, want only loading and bound messages", message.Kind())
			}
		}
		var holds []int
		for _, entry := range entries {
			holds = append(holds, entry.Hold)
			rhEqual(t, "members", shown.NamedBound(t, entry.Members), map[string]any{"label": "v2", "n": 0})
		}
		rhEqual(t, "holds", rhSorted(holds), []int{0, 1})
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › reloads during a full window: calls are answered by the old names until the fresh bound goes (audit A9)
	t.Run("reloads during a full window: calls are answered by the old names until the fresh bound goes (audit A9)", func(t *testing.T) {
		two := mustContract(contracts.Remote("app.two-variants", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
			Variants: []contracts.VariantEntry{
				contracts.Var("a", contracts.VariantDeclaration{
					State: []contracts.Property{rhProp("label", contracts.Str())},
					Methods: []contracts.MethodEntry{
						contracts.Meth("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
						contracts.Meth("ping", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
					},
				}),
				contracts.Var("b", contracts.VariantDeclaration{
					State:   []contracts.Property{rhProp("label", contracts.Str())},
					Methods: []contracts.MethodEntry{contracts.Meth("ping", contracts.NewMethod(contracts.Schema{}, contracts.Int()))},
				}),
			},
		}))
		ping := func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 7.0, nil }
		serveTwo := func(variant, label string) func(FacetHost) error {
			return func(h FacetHost) error {
				return h.Serve(two, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					doc := stateOf(self, rhCounter)
					bump := func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						err := doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
						if err != nil {
							return nil, err
						}
						return doc.Value().Value("n"), nil
					}
					members := contracts.VariantImplementation{
						State:   map[string]chord.Watchable[delta.JsonValue]{"label": rhConst(label)},
						Methods: map[string]contracts.MethodFunction{"ping": ping},
					}
					if variant == "a" {
						members.Methods["bump"] = bump
					}
					return contracts.Implementation{Variant: variant, Members: members}, nil
				}, ServeOptions{})
			}
		}
		server, peer := rhRawServer(t, serveTwo("a", "v1"), rawPeerOptions{NoAck: true})
		a := wireNames(t, two, "a")
		b := wireNames(t, two, "b")
		rhHold(peer, 0, two)
		peer.Next("bound")
		// A second message in flight: the window (two) is full.
		changed := func(h FacetHost) error {
			return h.Serve(two, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				return contracts.Implementation{Variant: "a", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{
						"label": self.Derive([]any{stateOf(self, rhCounter).s}, func(values []any) (delta.JsonValue, error) {
							return fmt.Sprintf("n %v", values[0].(*delta.JsonObject).Value("n")), nil
						}),
					},
					Methods: map[string]contracts.MethodFunction{
						"bump": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return 0.0, nil },
						"ping": ping,
					},
				}}, nil
			}, ServeOptions{})
		}
		rhReload(t, server, changed)
		peer.Next("bound")
		// Restarts while the window is full: the fresh bound waits for a slot, the hold answers by its old names.
		rhReload(t, server, serveTwo("a", "v2"))
		rhSync(t, peer)
		rhBumpCall(peer, 0, a.Methods["bump"])
		rhEqual(t, "bump", rhResultNumber(t, peer.Next("result")), 1)
		// The new variant lacks a method of the old names: the attempt refuses it.
		rhReload(t, server, serveTwo("b", "v3"))
		rhSync(t, peer)
		rhBumpCall(peer, 1, a.Methods["bump"])
		requireChordWire(t, peer.Next("error").(*link.ErrorMessage).Error, "invalid", "member")
		rhBumpCall(peer, 2, a.Methods["ping"])
		rhEqual(t, "ping", rhResultNumber(t, peer.Next("result")), 7)
		if got := countKind(peer, "bound"); got != 0 {
			t.Fatalf("bound messages before the ack = %d, want 0", got)
		}
		peer.Ack(link.Optional[int]{})
		fresh := peer.Next("bound").(*link.BoundMessage)
		if fresh.Holds[0].Hold != 0 || fresh.Holds[0].Variant != b.Variant {
			t.Fatalf("fresh bound = %+v, want hold 0 of variant b", fresh.Holds[0])
		}
		rhEqual(t, "members", b.NamedBound(t, fresh.Holds[0].Members), map[string]any{"label": "v3"})
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › collects the old instance while the hold's fresh bound waits for a slot (Astra, audit A9 GC)
	t.Run("collects the old instance while the hold's fresh bound waits for a slot (Astra, audit A9 GC)", func(t *testing.T) {
		typ := mustContract(contracts.Remote("astra.audit-gc", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
			Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
				State: []contracts.Property{rhProp("n", contracts.Int())},
			})},
		}))
		var declared Self
		var declaredMu sync.Mutex
		facet := func(n float64) func(FacetHost) error {
			return func(h FacetHost) error {
				return h.Serve(typ, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					declaredMu.Lock()
					declared = self
					declaredMu.Unlock()
					return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(n)},
					}}, nil
				}, ServeOptions{})
			}
		}
		server, peer := rhRawServer(t, facet(1), rawPeerOptions{NoAck: true})
		rhHold(peer, 0, typ)
		peer.Next("bound")
		// A second bound in flight: the window is full.
		_, err := server.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return facet(2)(h) })}}, background, ReloadOptions{})
		must(t, err)
		peer.Next("bound")
		declaredMu.Lock()
		retired := weak.Make(declared.(*attempt))
		declared = nil
		declaredMu.Unlock()
		_, err = server.Reload([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return facet(3)(h) })}}, background, ReloadOptions{})
		must(t, err)
		rhSync(t, peer)
		nexttest.Flushed()
		// Its fresh bound waits for an ack; the hold keeps the old names for calls, not the old instance.
		if got := countKind(peer, "bound"); got != 0 {
			t.Fatalf("bound messages before the ack = %d, want 0", got)
		}
		declaredMu.Lock()
		declared = nil
		declaredMu.Unlock()
		expectCollected(t, retired)
		peer.Ack(link.Optional[int]{})
		peer.Next("bound")
	})

	// hold.test.ts: holds across a reload (slice 7b §6) › through a relay: a worker's reload reaches the tab as it reaches a direct consumer
	t.Run("through a relay: a worker's reload reaches the tab as it reaches a direct consumer", func(t *testing.T) {
		rel := relay(t, relayedOptions{})
		notes := refGet(t, rel.Tab, PlacedNotes, jsonObject("session", "S1"))
		worker := rel.Worker("session|S1")
		watch, err := refStateWatch(notes, background)
		must(t, err)
		states := statesOf(t, watch)
		_, err = worker.Reload(nil, background, ReloadOptions{})
		must(t, err)
		rel.Transported()
		if got := rhState(notes); got.Kind != RefStateFailed || got.Cause.Kind != data.FailureWaiting {
			t.Fatalf("state = %+v, want failed waiting", got)
		}
		_, err = worker.Reload([]HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			_, err := serveNotes(h, PlacedNotes)
			return err
		})}}, background, ReloadOptions{})
		must(t, err)
		rel.Transported()
		// The reload publishes the type, and the object the waiting hold asks for activates after it returned: in Pi that is
		// microtasks, which `transported()` drains; here it is an owned task, so the tab's reference is awaited live, and
		// the frames of its state watch delivered.
		awaitLive(t, notes)
		deliveredOn(rel.Tab)
		if kind := rhRefState(notes); kind != RefStateLive {
			t.Fatalf("state = %v, want live", kind)
		}
		if all := states.All(); len(all) == 0 || all[len(all)-1] != "live" {
			t.Fatalf("states = %v, want the last live", all)
		}
	})
}

// hold.test.ts: holds across more than one reload (fix round 1) › keeps a real subscriber when a changed constant later becomes dynamic
func TestHoldsAcrossMoreThanOneSwapKeepsARealSubscriberWhenAChangedConstantLaterBecomesDynamic(t *testing.T) {
	kind := mustKind(contracts.NewHostKind("sol-server"))
	typ := mustContract(contracts.Remote("sol.wire", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return kind.Address(nil), nil },
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
			State:   []contracts.Property{rhProp("x", contracts.Int())},
			Methods: []contracts.MethodEntry{contracts.Meth("set", contracts.NewMethod(contracts.Int()))},
		})},
	}))
	doc, err := documents.DefineState("wire", documents.StateOptions{
		Schema: contracts.Obj(contracts.Prop("n", contracts.Int())), Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", 0.0) },
	})
	must(t, err)
	facet := func(constant *float64) func(FacetHost) error {
		return func(h FacetHost) error {
			return h.Serve(typ, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				wire := stateOf(self, doc)
				var x chord.Watchable[delta.JsonValue]
				if constant != nil {
					x = rhConst(*constant)
				} else {
					x = self.Derive([]any{wire.s}, func(values []any) (delta.JsonValue, error) {
						return values[0].(*delta.JsonObject).Value("n"), nil
					})
				}
				return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"x": x},
					Methods: map[string]contracts.MethodFunction{"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, wire.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
					}},
				}}, nil
			}, ServeOptions{})
		}
	}
	reload := func(server Host, constant *float64) {
		_, err := server.Reload([]HostFacet{{Name: "@sol/wire", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return facet(constant)(h) })}},
			background, ReloadOptions{})
		must(t, err)
	}
	server := newHost(t, HostOptions{Address: new(kind.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	tab := newHost(t, HostOptions{OnError: func(HostError) {}})
	one, two := 1.0, 2.0
	mustLoad(t)(server.Load([]HostFacet{{Name: "@sol/wire", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return facet(&one)(h) })}}, background))
	mustLoad(t)(tab.Load(nil, background))
	up, down := link.MemoryLinkPair()
	must(t, server.Accept(up, LinkOptions{Principal: ownerPrincipal()}, background))
	must(t, tab.Connect(down, LinkOptions{Principal: serverPrincipal()}, background))
	held := refGet(t, tab, typ, jsonObject())
	// Each reload restarts the object: the tab sees the new attempt's values once it is live.
	settled := func(want any) {
		t.Helper()
		scopeEventually(t, "the tab shows the new code's value", func() bool {
			transported(up, down)
			deliveredOn(server, tab)
			value, err := held.Member("x").Value()
			return err == nil && value == want
		})
	}
	reload(server, &two)
	settled(2.0)
	reload(server, nil)
	settled(0.0)
	_, err = held.Call("set", 7.0, background)
	must(t, err)
	settled(7.0)
}

// hold.test.ts: a hold after its object's live failure (final review) › follows a member re-exposing another object's source until the admitted commits published (audit A10)
func TestAHoldAfterItsObjectsLiveFailureFollowsAMemberReExposingAnotherObjectsSourceUntilTheAdmittedCommitsPublished(t *testing.T) {
	point := contracts.Obj(contracts.Prop("n", contracts.Int()))
	a := mustContract(contracts.Local("app.foreign", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("x", point)},
	}))
	b := mustContract(contracts.Remote("app.reexposing", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{rhProp("x", point)}})},
	}))
	var mu sync.Mutex
	var foreign state.Writable[any]
	var selfB Self
	s := linked(t, func(h FacetHost) error {
		if err := h.Serve(a, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			w := self.Writable(jsonObject("n", 0.0))
			mu.Lock()
			foreign = w
			mu.Unlock()
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"x": self.Derive([]any{w}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
			}}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(b, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			mu.Lock()
			selfB = self
			mu.Unlock()
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"x": Expose(self.Get(a, jsonObject()).Member("x"))},
			}}, nil
		}, ServeOptions{})
	}, linkedOptions{})
	ref := refGet(t, s.Client, b, jsonObject())
	calls := nexttest.ChangesOf(rhWatch(t, ref.Member("x")))
	mu.Lock()
	failing, writable := selfB, foreign
	mu.Unlock()
	// Pi's `failLive(...); foreign.set(...)` is one synchronous block: the failed object's stop of following (a task of
	// the loop in Go) cannot land between the two, so they are one section.
	var setErr error
	failLiveThen(s.Server, failing, data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "boom"}, nil), func() {
		setErr = writable.Set(jsonObject("n", 1.0))
	})
	// A's change lands after B failed, in the window before B's admitted commits published: it shows (one more
	// change), then the member holds with the failure.
	must(t, setErr)
	rhLinks(t, s)
	rhEqual(t, "x", memberValue(t, ref, "x"), map[string]any{"n": 1})
	items := calls.Items()
	if len(items) != 2 || items[0].Kind != chord.WatchCallValue || items[1].Kind != chord.WatchCallFailed ||
		!strings.Contains(items[1].Error.Error(), "boom") {
		t.Fatalf("calls = %v, want a value then a failure boom", nexttest.ValuesOf(items))
	}
	rhEqual(t, "first", items[0].Value, map[string]any{"n": 1})
}

// hold.test.ts: a hold across a reload whose mapping fails (final review) › gets the member's failure in its fresh bound and heals with the new source (Sol, audit A9)
func TestAHoldAcrossASwapWhoseMappingFailsGetsTheMembersFailureInItsFreshBoundAndHealsWithTheNewSource(t *testing.T) {
	w1 := mustContract(contracts.Remote("app.swap-failed", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
		Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{rhProp("x", contracts.Int())}})},
	}))
	w2 := mustContract(contracts.Evolve(w1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("main", "x", contracts.Obj(contracts.Prop("n", contracts.Int())))
	}))
	var throws atomic.Bool
	throws.Store(true)
	var mu sync.Mutex
	var point state.Writable[any]
	serve := func(h FacetHost, mapping contracts.Mapping) error {
		return h.Serve(w2, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			w := self.Writable(jsonObject("n", 1.0))
			mu.Lock()
			point = w
			mu.Unlock()
			return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"x": self.Derive([]any{w}, func(values []any) (delta.JsonValue, error) { return values[0], nil }),
				},
			}}, nil
		}, ServeOptions{Accepts: contracts.AcceptsTable{{Name: "1", Value: contracts.VersionMappings{
			VariantState: []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{{Name: "main", Value: []contracts.Entry[contracts.StateMapping]{
				{Name: "x", Value: contracts.StateMapping{Func: mapping}},
			}}},
		}}}})
	}
	n := func(value delta.JsonValue) (delta.JsonValue, error) { return value.(*delta.JsonObject).Value("n"), nil }
	s := linked(t, func(h FacetHost) error { return serve(h, n) }, linkedOptions{})
	ref := refGet(t, s.Client, w1, jsonObject())
	calls := nexttest.ChangesOf(rhWatch(t, ref.Member("x")))
	// The new instance's mapping throws for its first value.
	failing := func(value delta.JsonValue) (delta.JsonValue, error) {
		if throws.Load() {
			return nil, fmt.Errorf("member boom")
		}
		return value.(*delta.JsonObject).Value("n"), nil
	}
	rhReload(t, s.Server, func(h FacetHost) error { return serve(h, failing) })
	rhLinks(t, s)
	// Live, the member failed: its watch got the failure, a read throws it (as a failed member does on the host).
	items := calls.Items()
	if kind := rhRefState(ref); kind != RefStateLive || len(items) != 1 || items[0].Kind != chord.WatchCallFailed ||
		!strings.Contains(items[0].Error.Error(), "member boom") {
		t.Fatalf("state %v, calls %v; want live and one failure member boom", rhRefState(ref), nexttest.ValuesOf(items))
	}
	if _, err := ref.Member("x").Value(); err == nil || !strings.Contains(err.Error(), "member boom") {
		t.Fatalf("read error = %v, want member boom", err)
	}
	// It follows the new source: the next value heals it.
	throws.Store(false)
	mu.Lock()
	writable := point
	mu.Unlock()
	must(t, rhSet(s.Server, writable, jsonObject("n", 3.0)))
	rhLinks(t, s)
	rhEqual(t, "x", memberValue(t, ref, "x"), 3)
	items = calls.Items()
	if len(items) != 2 || items[1].Kind != chord.WatchCallValue {
		t.Fatalf("calls = %v, want a failure then a value", nexttest.ValuesOf(items))
	}
	rhEqual(t, "healed", items[1].Value, 3)
}

// hold.test.ts: holds across a reload (slice 7b §6) › a hold waiting for a window slot whose slot frees between the old attempt's seal and its end binds safely
func TestHoldWaitingForAWindowSlotWhoseSlotFreesBetweenTheOldAttemptsSealAndItsEndBindsSafely(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		window := mustContract(contracts.Remote("app.window", contracts.RemoteDeclaration{
			Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
			Variants: []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{
				State: []contracts.Property{rhProp("n", contracts.Int())},
				Methods: []contracts.MethodEntry{
					contracts.Meth("bump", contracts.NewMethod(contracts.Schema{}, contracts.Int())),
					contracts.Meth("slow", contracts.NewMethod(contracts.Schema{}, contracts.Str())),
					contracts.Meth("start", contracts.NewMethod()),
				},
			})},
		}))
		wait, err := DefineJob("app.window-wait", JobOptions{Version: 1, Args: contracts.Obj(), Result: contracts.Str()})
		must(t, err)
		gate, slowEntered := newSignal(), newSignal()
		// atSeal is called once the old attempt's seal cut the job's run: the test frees the window's slots then.
		var atSeal atomic.Pointer[func()]
		serveWindow := func(code string) func(FacetHost) error {
			return func(h FacetHost) error {
				return h.Serve(window, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					self.Open(func(context.Context) (any, error) { return code, nil },
						func(any, NativeCloseReason, context.Context) error { return nil })
					doc := stateOf(self, rhCounter)
					job := self.Job(wait, JobImplementation{Run: func(_ any, ctx JobCtx) (any, error) {
						<-ctx.Done()
						if fn := atSeal.Load(); fn != nil {
							(*fn)()
						}
						return "cut", nil
					}})
					return contracts.Implementation{Variant: "main", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"n": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
								return values[0].(*delta.JsonObject).Value("n"), nil
							}),
						},
						Methods: map[string]contracts.MethodFunction{
							"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								if err := doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx); err != nil {
									return nil, err
								}
								return doc.Value().Value("n"), nil
							},
							"start": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								_, err := job.Start(jsonObject(), ctx, StartOptions{})
								return nil, err
							},
							"slow": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
								if code == "v1" {
									slowEntered.fire()
								}
								gate.await(t, ctx)
								return code + " slow", nil
							},
						},
					}}, nil
				}, ServeOptions{})
			}
		}
		errs := &hostErrors{}
		server := newHost(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: errs.push})
		mustLoad(t)(server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return serveWindow("v1")(h) })}}, background))
		a, b := link.MemoryLinkPair()
		must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
		peer := newRawPeer(t, b, rawPeerOptions{NoAck: true})
		names := wireNames(t, window, "main")
		peer.Next("hello")
		rhHold(peer, 0, window)
		peer.Next("bound")
		peer.Ack(link.Optional[int]{})
		rhBumpCall(peer, 0, names.Methods["start"])
		peer.Next("result")
		// Two messages unacknowledged: the window (two) is full.
		rhBumpCall(peer, 1, names.Methods["bump"])
		peer.Next("state")
		rhBumpCall(peer, 2, names.Methods["bump"])
		peer.Next("result")
		peer.Next("result")
		settle(peer)
		// A running call keeps the old attempt open; the acks come right at its seal (the job's run is cut there).
		rhBumpCall(peer, 3, names.Methods["slow"])
		slowEntered.wait(t)
		release := func() {
			peer.Ack(link.Optional[int]{})
			peer.Ack(link.Optional[int]{})
		}
		atSeal.Store(&release)
		rhReload(t, server, serveWindow("v2"))
		// A second hold during the drain: it binds the old attempt shown live, but waits for a slot.
		rhHold(peer, 1, window)
		settle(peer)
		gate.fire()
		if got := rhResultNumber(t, peer.Next("result")); got != "v1 slow" {
			t.Fatalf("slow = %q, want \"v1 slow\"", got)
		}
		settle(peer)
		settle(peer)
		for _, err := range errs.All() {
			if err.Kind == ErrProtocolError {
				t.Fatalf("a protocol error was reported: %v", err)
			}
		}
		if peer.Closed() {
			t.Fatalf("the link closed")
		}
		// Both holds end on the new code (an ack that crosses nothing is ignored, so acking again is safe).
		bound := map[int]bool{}
		scopeEventually(t, "both holds bound on the new code", func() bool {
			peer.Ack(link.Optional[int]{})
			settle(peer)
			for _, message := range peer.Received() {
				if m, ok := message.(*link.BoundMessage); ok {
					for _, entry := range m.Holds {
						bound[entry.Hold] = true
					}
				}
			}
			return bound[0] && bound[1]
		})
	})
}
