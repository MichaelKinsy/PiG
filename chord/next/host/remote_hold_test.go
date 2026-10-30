package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

// Ports packages/chord/test/next/remote/hold.test.ts.
//
// Go forms (rows of the lane): `instancesOf(Hold, "start")`, `instancesOf(ChordObject, ...)` and the mocked Delta
// `encoder` are designed out: a Go test reaches the holds, their members' encoders and the objects through the host's
// own links and objects (rhHolds, rhObjects) and checks collection with weak pointers (nexttest.ExpectCollected).
// `await transported()` is rhSync: a `ping` after the messages sent and its `pong` back (the channel handles a link's
// messages in order). Pi's `fakeTimers()` is a host whose loop is a scopeLoop (virtual clock); its `advanceTimersByTimeAsync`
// is rhVirtual.advance.

// --- support ---------------------------------------------------------------------------------------------------------

func rhJSON(t testing.TB, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	must(t, err)
	return string(encoded)
}

// rhWire is a wire error as JSON text.
func rhWire(t testing.TB, wire link.WireError) string { return rhJSON(t, wire) }

// rhSync returns once the host handled every message the peer sent before it (rhSync is Pi's `await transported()` for
// the messages a raw peer sent; what the host sent back is in the peer's received list when it returns).
func rhSync(t testing.TB, peer *RawPeer) {
	t.Helper()
	peer.Send(&link.PingMessage{})
	peer.Next("pong")
}

func rhEqual(t testing.TB, what string, got, want any) {
	t.Helper()
	if g, w := rhJSON(t, got), rhJSON(t, want); g != w {
		t.Fatalf("%s = %s, want %s", what, g, w)
	}
}

func rhCreate(t testing.TB, options HostOptions) Host { return newHost(t, options) }

func rhProp(name string, schema contracts.TSchema) contracts.Property {
	return contracts.Prop(name, schema)
}

var rhCounter = func() *documents.StateDefinition {
	definition, err := documents.DefineState("n", documents.StateOptions{
		Schema:  contracts.Obj(contracts.Prop("n", contracts.Int())),
		Version: 1,
		Initial: func() *delta.JsonObject { return jsonObject("n", float64(0)) },
	})
	if err != nil {
		panic(err)
	}
	return definition
}()

// rhRemote is `remote(name, { version: 1, params: {}, hostAddress: () => Server(), variants: { v: ... } })`.
func rhRemote(name string, state []contracts.Property, methods ...contracts.MethodEntry) *contracts.Contract {
	return mustContract(contracts.Remote(name, contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: exServerAddress,
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: state, Methods: methods})},
	}))
}

// rhHalves is a remote type whose `half` member throws once `n` is 2: a member failing after the object is live (W1).
var rhHalves = rhRemote("app.halves",
	[]contracts.Property{rhProp("n", contracts.Obj(contracts.Prop("n", contracts.Int()))), rhProp("half", contracts.Int())},
	contracts.Meth("set", contracts.NewMethod(contracts.Int())), contracts.Meth("wait", contracts.NewMethod()))

func rhConst(value any) chord.Watchable[delta.JsonValue] {
	return Expose(state.NewConstant(value))
}

func rhServeHalves(host FacetHost) error {
	return host.Serve(rhHalves, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		n := stateOf(self, rhCounter)
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{
				"n": n.Member(),
				"half": self.Derive([]any{n.s}, func(values []any) (delta.JsonValue, error) {
					count := values[0].(*delta.JsonObject).Value("n").(float64)
					if count == 2 {
						return nil, errors.New("half threw")
					}
					return count / 2, nil
				}),
			},
			Methods: map[string]contracts.MethodFunction{
				"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, n.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
				},
				"wait": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					<-ctx.Done()
					return nil, nil
				},
			},
		}}, nil
	}, ServeOptions{})
}

// rhHalvesRig is a server serving rhHalves and a raw peer; set writes `n` through hold 0.
type rhHalvesRig struct {
	*rawLinkedHost
	t     testing.TB
	calls int
}

func rhHalvesPeer(t testing.TB) *rhHalvesRig {
	t.Helper()
	return &rhHalvesRig{rawLinkedHost: rawLinked(t, rhServeHalves, rawLinkedOptions{}), t: t}
}

func (r *rhHalvesRig) set(value int) {
	r.t.Helper()
	call := r.calls
	r.calls++
	part, err := link.JsonPart(float64(value))
	must(r.t, err)
	r.Peer.Send(&link.CallMessage{Call: call, Hold: 0, Method: wireNames(r.t, rhHalves, "v").Methods["set"], Args: part})
	if got := r.Peer.Next("result").(*link.ResultMessage).Call; got != call {
		r.t.Fatalf("result for call %d, want %d", got, call)
	}
}

func rhHold(peer *RawPeer, id int, typ *contracts.Contract) {
	peer.Send(&link.HoldMessage{
		Hold: id, Type: typ.Name, Version: typ.Version, Fingerprint: typ.Fingerprint, Params: link.Text("{}"),
		Address: serverWireAddress,
	})
}

func rhNamedState(t testing.TB, names wireIndexes, entry link.StateEntry) map[string]any {
	return names.NamedState(t, entry.Members)
}

func rhFailed(t testing.TB, failed []link.FailedMember) string {
	t.Helper()
	parts := make([]string, len(failed))
	for i, f := range failed {
		parts[i] = fmt.Sprintf("%d:%s", f.Member, rhWire(t, f.Error))
	}
	return strings.Join(parts, ",")
}

func rhPluginFailure(member int, message string) string {
	return fmt.Sprintf(`%d:{"kind":"plugin","code":"internal","message":%q}`, member, message)
}

// rhHolds are the holds a host's links serve, in the order of its links.
func rhHolds(host Host) []*hold {
	var holds []*hold
	h := host.(*chordHost)
	h.loop().Run(func() {
		for _, l := range h.links {
			for _, entry := range l.providers.values() {
				for _, e := range entry.entries.values() {
					if served, ok := e.(*hold); ok {
						holds = append(holds, served)
					}
				}
			}
		}
	})
	return holds
}

// rhQuiesce waits until the host's loop has finished every task it owns (the teardown of an ended object runs on one), so
// what a test checks for collection is not still held by a goroutine on its way out.
func rhQuiesce(host Host) { host.(*chordHost).loop().(*Loop).Wait() }

func rhObjects(host Host) []*chordObject {
	var objects []*chordObject
	h := host.(*chordHost)
	h.loop().Run(func() { objects = h.objectList() })
	return objects
}

// rhEncoders are the members' encoders of bound holds.
func rhEncoders(holds []*hold) []weak.Pointer[delta.Encoder] {
	var refs []weak.Pointer[delta.Encoder]
	for _, h := range holds {
		for _, member := range h.state.followed {
			refs = append(refs, weak.Make(member.encoder))
		}
	}
	return refs
}

func rhWeak[T any](items []*T) []weak.Pointer[T] {
	refs := make([]weak.Pointer[T], len(items))
	for i, item := range items {
		refs[i] = weak.Make(item)
	}
	return refs
}

// rhVirtual is a host on virtual time (Pi's `fakeTimers()`): a scopeLoop as its loop.
type rhVirtual struct {
	loop *scopeLoop
}

func newRhVirtual() *rhVirtual { return &rhVirtual{loop: newScopeLoop()} }

// advance is `vi.advanceTimersByTimeAsync(ms)`: timers due run as sections, and the microtasks they queue.
func (v *rhVirtual) advance(ms int64) {
	v.loop.AdvanceAsync(ms)
	v.loop.barrier()
}

// rawServer is a server host on the virtual loop with setup loaded, and a raw peer on a link it accepted as child.
func (v *rhVirtual) rawServer(t testing.TB, setup func(host FacetHost) error) (Host, *RawPeer, *hostErrors) {
	t.Helper()
	errs := &hostErrors{}
	server := rhCreate(t, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: errs.push, loop: v.loop,
	})
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return setup(host)
	})}}, background))
	return server, rhSecondPeer(t, server), errs
}

// rhSecondPeer is a raw peer on a link server accepted as child, stamped with the owner principal.
func rhSecondPeer(t testing.TB, server Host) *RawPeer {
	t.Helper()
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	return newRawPeer(t, b, rawPeerOptions{})
}

// rhFlaky is a remote type whose open throws `offline` (broken when broken) while left is above 0.
var rhFlaky = rhRemote("app.flaky", []contracts.Property{rhProp("n", contracts.Int())})

type rhFailing struct {
	mu     sync.Mutex
	left   int
	broken bool
}

func (f *rhFailing) next() (fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.left--
	return f.left+1 > 0
}

func rhServeFlaky(failing *rhFailing) func(FacetHost) error {
	return func(host FacetHost) error {
		return host.Serve(rhFlaky, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) {
				if !failing.next() {
					return nil, nil
				}
				if failing.broken {
					return nil, data.Broken(errors.New("offline"))
				}
				return nil, errors.New("offline")
			}, func(any, NativeCloseReason, context.Context) error { return nil })
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(1)},
			}}, nil
		}, ServeOptions{})
	}
}

// --- the parts of a hold ---------------------------------------------------------------------------------------------

func TestHoldParts(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: the parts of a hold › are its members, each with its own encoder: the snapshot by member, then the changed members' ops
		t.Run("are its members, each with its own encoder: the snapshot by member, then the changed members' ops", func(t *testing.T) {
			rig := rhHalvesPeer(t)
			names := wireNames(t, rhHalves, "v")
			rhHold(rig.Peer, 0, rhHalves)
			rhEqual(t, "bound", names.NamedBound(t, rig.Peer.Next("bound").(*link.BoundMessage).Holds[0].Members), map[string]any{
				"n": map[string]any{"n": 0}, "half": 0,
			})
			rig.set(1)
			first := rhNamedState(t, names, rig.Peer.Next("state").(*link.StateMessage).Holds[0])
			rhEqual(t, "first", first, map[string]any{"n": []any{[]any{"s", []any{"n"}, 1}}, "half": []any{[]any{"r", 0.5}}})
			rig.set(4)
			// The member's encoder interns a path on its second use.
			decoder := delta.NewDecoder()
			_, err := decoder.Decode(rhWireOps(first["n"]))
			must(t, err)
			second := rhNamedState(t, names, rig.Peer.Next("state").(*link.StateMessage).Holds[0])
			ops, err := decoder.Decode(rhWireOps(second["n"]))
			must(t, err)
			rhEqual(t, "decoded", ops, []any{[]any{"s", []any{"n"}, 4}})
		})
	})
}

// rhWireOps is a parsed ops part as wire ops.
func rhWireOps(part any) []delta.WireOp {
	list := part.([]any)
	ops := make([]delta.WireOp, len(list))
	for i, op := range list {
		ops[i] = delta.WireOp(op.([]any))
	}
	return ops
}

func TestHoldFailedMembers(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: failed members › are listed in the header of the state that carries the failure, never in its part; they recover in the same hold
		t.Run("are listed in the header of the state that carries the failure, never in its part; they recover in the same hold", func(t *testing.T) {
			rig := rhHalvesPeer(t)
			names := wireNames(t, rhHalves, "v")
			rhHold(rig.Peer, 0, rhHalves)
			rhHold(rig.Peer, 1, rhHalves)
			rig.Peer.Next("bound")
			rig.Peer.Next("bound")
			rig.set(2)
			state := rig.Peer.Next("state").(*link.StateMessage)
			want := rhPluginFailure(names.State["half"], "half threw")
			// Race 12: each hold's entry carries its healthy members' ops and the failure.
			if len(state.Holds) != 2 || state.Holds[0].Hold != 0 || state.Holds[1].Hold != 1 {
				t.Fatalf("holds = %v", state.Holds)
			}
			for _, entry := range state.Holds {
				if got := rhFailed(t, entry.Failed); got != want {
					t.Fatalf("failed = %s, want %s", got, want)
				}
				rhEqual(t, "members", rhNamedState(t, names, entry), map[string]any{"n": []any{[]any{"s", []any{"n"}, 2}}})
			}
			rig.set(3)
			next := rig.Peer.Next("state").(*link.StateMessage)
			if len(next.Holds) != 2 || len(next.Holds[0].Failed) != 0 || len(next.Holds[1].Failed) != 0 {
				t.Fatalf("holds = %v", next.Holds)
			}
			// The derive healed: its next value replaces the root.
			rhEqual(t, "half", rhNamedState(t, names, next.Holds[0])["half"], []any{[]any{"r", 1.5}})
		})

		// hold.test.ts: failed members › reach a client's watch over the link as one failure call, then the healed value: the watch stays open
		t.Run("reach a client's watch over the link as one failure call, then the healed value: the watch stays open", func(t *testing.T) {
			hosts := linked(t, rhServeHalves, linkedOptions{})
			halves := exGet(t, hosts.Client, rhHalves, jsonObject())
			calls := nexttest.ChangesOf(rhWatch(t, halves.Member("half")))
			_, err := halves.Call("set", float64(2), background)
			must(t, err)
			transported(hosts.Links[0], hosts.Links[1])
			rhWaitCalls(t, calls, 1)
			got := calls.Items()
			if len(got) != 1 || got[0].Kind != chord.WatchCallFailed || !strings.Contains(got[0].Error.Error(), "half threw") {
				t.Fatalf("calls = %v", got)
			}
			_, err = halves.Call("set", float64(6), background)
			must(t, err)
			transported(hosts.Links[0], hosts.Links[1])
			rhWaitCalls(t, calls, 2)
			value, err := halves.Member("half").Value()
			must(t, err)
			rhEqual(t, "half.value", value, 3)
			last := calls.Items()[calls.Len()-1]
			rhEqual(t, "last call", []any{last.Kind, last.Value, last.Ops, last.Retired}, []any{"value", 3, []any{[]any{"r", 3}}, false})
			must(t, hosts.Client.Close(background))
			must(t, hosts.Server.Close(background))
		})

		// hold.test.ts: failed members › are listed in bound for a hold made after the failure
		t.Run("are listed in bound for a hold made after the failure", func(t *testing.T) {
			rig := rhHalvesPeer(t)
			names := wireNames(t, rhHalves, "v")
			rhHold(rig.Peer, 0, rhHalves)
			rig.Peer.Next("bound")
			rig.set(2)
			rig.Peer.Next("state")
			rhHold(rig.Peer, 1, rhHalves)
			bound := rig.Peer.Next("bound").(*link.BoundMessage).Holds[0]
			if got, want := rhFailed(t, bound.Failed), rhPluginFailure(names.State["half"], "half threw"); got != want {
				t.Fatalf("failed = %s, want %s", got, want)
			}
			rhEqual(t, "members", names.NamedBound(t, bound.Members), map[string]any{"n": map[string]any{"n": 2}})
			// Followed though it failed at the snapshot: it recovers in this hold.
			rig.set(4)
			next := rig.Peer.Next("state").(*link.StateMessage)
			at := slices.IndexFunc(next.Holds, func(entry link.StateEntry) bool { return entry.Hold == 1 })
			rhEqual(t, "half", rhNamedState(t, names, next.Holds[at])["half"], []any{[]any{"r", 2}})
		})
	})
}

// rhWatch is `await ref.member.watch(context)`.
func rhWatch(t testing.TB, member *ReferenceMember) chord.WatchHandle[delta.JsonValue] {
	t.Helper()
	watch, err := member.Watch(background)
	must(t, err)
	return watch
}

func TestHoldTransientFailures(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: transient failures › a new hold of an object failed transiently is sent the failure at once
		t.Run("a new hold of an object failed transiently is sent the failure at once", func(t *testing.T) {
			// Timers stand still: the failed object's backoff window does not end while the test runs.
			virtual := newRhVirtual()
			_, peer, _ := virtual.rawServer(t, rhServeFlaky(&rhFailing{left: 1000}))
			rhHold(peer, 0, rhFlaky)
			want := `{"kind":"transient","error":{"kind":"chord","error":{"code":"unavailable","message":"app.flaky: offline"}}}`
			failed := peer.Next("failed").(*link.FailedMessage)
			if got := rhJSON(t, []any{failed.Hold, failed.Cause.Kind, failed.Cause.Error}); !strings.Contains(got, `"transient"`) || !strings.Contains(got, "app.flaky: offline") {
				t.Fatalf("failed = %s, want cause %s", got, want)
			}
			rhHold(peer, 1, rhFlaky)
			// The object stays failed for its backoff window (1 s); the new hold does not wait for the next attempt.
			second := peer.Next("failed").(*link.FailedMessage)
			if second.Hold != 1 || second.Cause.Kind != "transient" {
				t.Fatalf("second = %+v", second)
			}
		})
	})
}

// --- GC --------------------------------------------------------------------------------------------------------------

func TestHoldGC(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: GC › a closed link retained by the test keeps no hold, encoder or object
		t.Run("a closed link retained by the test keeps no hold, encoder or object", func(t *testing.T) {
			rig := rhHalvesPeer(t)
			a, b := link.MemoryLinkPair()
			must(t, rig.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			peer := newRawPeer(t, b, rawPeerOptions{})
			before := rhHolds(rig.Server)
			rhHold(peer, 0, rhHalves)
			peer.Next("bound")
			var holds []*hold
			for _, h := range rhHolds(rig.Server) {
				if !slices.Contains(before, h) {
					holds = append(holds, h)
				}
			}
			objects := rhObjects(rig.Server)
			encoders := rhEncoders(holds)
			holdRefs, objectRefs := rhWeak(holds), rhWeak(objects)
			if len(holdRefs)+len(objectRefs)+len(encoders) != 4 {
				t.Fatalf("refs = %d holds, %d objects, %d encoders; want 1, 1, 2", len(holdRefs), len(objectRefs), len(encoders))
			}
			peer.Close()
			transported(a, b)
			// The object ends with its last hold: it leaves the host, then its teardown task finishes.
			rhWaitFor(t, func() bool { return len(rhObjects(rig.Server)) == 0 })
			rhQuiesce(rig.Server)
			nexttest.ExpectCollected(t, holdRefs...)
			nexttest.ExpectCollected(t, encoders...)
			nexttest.ExpectCollected(t, objectRefs...)
			runtime.KeepAlive(a)
			runtime.KeepAlive(rig.Server)
		})
	})
}

// rhPending is a remote type whose activation never completes: its open waits for its abort.
var rhPending = rhRemote("app.pending", []contracts.Property{rhProp("n", contracts.Int())})

func rhServePending(host FacetHost) error {
	return host.Serve(rhPending, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		self.Open(func(ctx context.Context) (any, error) {
			<-ctx.Done()
			return nil, nil
		}, func(any, NativeCloseReason, context.Context) error { return nil })
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(1)},
		}}, nil
	}, ServeOptions{})
}

func TestHoldGCPending(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: GC › a released pending hold is kept neither by its object, still activating for another hold, nor by its waits
		t.Run("a released pending hold is kept neither by its object, still activating for another hold, nor by its waits", func(t *testing.T) {
			rig := rawLinked(t, rhServePending, rawLinkedOptions{})
			rhHold(rig.Peer, 0, rhPending)
			rhSync(t, rig.Peer)
			before := rhHolds(rig.Server)
			rhHold(rig.Peer, 1, rhPending)
			rhSync(t, rig.Peer)
			var made []*hold
			for _, h := range rhHolds(rig.Server) {
				if !slices.Contains(before, h) {
					made = append(made, h)
				}
			}
			if len(made) != 1 {
				t.Fatalf("made %d holds, want 1", len(made))
			}
			refs := rhWeak(made)
			rig.Peer.Send(&link.ReleaseMessage{Hold: 1})
			rhSync(t, rig.Peer)
			nexttest.ExpectCollected(t, refs...)
			runtime.KeepAlive(rig.Server)
		})

		// hold.test.ts: GC › a pending activation on a closed link keeps no hold
		t.Run("a pending activation on a closed link keeps no hold", func(t *testing.T) {
			rig := rawLinked(t, rhServePending, rawLinkedOptions{})
			a, b := link.MemoryLinkPair()
			must(t, rig.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			peer := newRawPeer(t, b, rawPeerOptions{})
			before := rhHolds(rig.Server)
			rhHold(peer, 0, rhPending)
			rhSync(t, peer)
			var made []*hold
			for _, h := range rhHolds(rig.Server) {
				if !slices.Contains(before, h) {
					made = append(made, h)
				}
			}
			refs := rhWeak(made)
			peer.Close()
			transported(a, b)
			nexttest.ExpectCollected(t, refs...)
			runtime.KeepAlive(a)
			runtime.KeepAlive(rig.Server)
		})

		// hold.test.ts: GC › a hold released while waiting for load is not kept by the wait
		t.Run("a hold released while waiting for load is not kept by the wait", func(t *testing.T) {
			slow := mustContract(contracts.Local("app.slow", contracts.LocalDeclaration{Params: contracts.Params{},
				Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
			}))
			timeout := 2 * time.Second
			server := rhCreate(t, HostOptions{
				Address: new(Server.Address(nil)), Principal: serverPrincipal(), ReadyTimeout: &timeout, OnError: func(HostError) {},
			})
			loadDone := make(chan error, 1)
			go func() {
				// A host-lifetime object whose open never resolves: `load` waits for it (until its readyTimeout).
				loadDone <- server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
					if err := rhServePending(host); err != nil {
						return err
					}
					return host.Serve(slow, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
						self.Open(func(ctx context.Context) (any, error) {
							<-ctx.Done()
							return nil, nil
						}, func(any, NativeCloseReason, context.Context) error { return nil })
						return contracts.Implementation{Members: contracts.VariantImplementation{
							State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(1)},
						}}, nil
					}, ServeOptions{})
				})}}, background)
			}()
			peer := rhSecondPeer(t, server)
			before := rhHolds(server)
			rhHold(peer, 0, rhPending)
			rhSync(t, peer)
			var made []*hold
			for _, h := range rhHolds(server) {
				if !slices.Contains(before, h) {
					made = append(made, h)
				}
			}
			peer.Send(&link.ReleaseMessage{Hold: 0})
			rhSync(t, peer)
			if len(made) != 1 {
				t.Fatalf("made %d holds, want 1", len(made))
			}
			refs := rhWeak(made)
			nexttest.ExpectCollected(t, refs...)
			runtime.KeepAlive(server)
			<-loadDone
		})
	})
}

// --- notices of a failed object --------------------------------------------------------------------------------------

func rhKindsAre(t testing.TB, peer *RawPeer, want ...string) {
	t.Helper()
	if got := peer.Kinds(); !slices.Equal(got, want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
}

func rhOfflineFailed(t testing.TB, message link.Message, hold int, kind string) *link.FailedMessage {
	t.Helper()
	failed := message.(*link.FailedMessage)
	want := `{"kind":"chord","error":{"code":"unavailable","message":"app.flaky: offline"}}`
	if failed.Hold != hold || failed.Cause.Kind != kind || rhWire(t, failed.Cause.Error) != want {
		t.Fatalf("failed = hold %d %s %s, want hold %d %s %s", failed.Hold, failed.Cause.Kind, rhWire(t, failed.Cause.Error), hold, kind, want)
	}
	return failed
}

func TestHoldNoticesOfAFailedObject(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: notices of a failed object › its holds get failed with the rest of its window, loading for each attempt, then bound (race 6)
		t.Run("its holds get failed with the rest of its window, loading for each attempt, then bound (race 6)", func(t *testing.T) {
			virtual := newRhVirtual()
			_, peer, _ := virtual.rawServer(t, rhServeFlaky(&rhFailing{left: 2}))
			rhHold(peer, 0, rhFlaky)
			first := rhOfflineFailed(t, peer.Next("failed"), 0, "transient")
			if !first.RetryIn.Ok || first.RetryIn.Value != 1000 {
				t.Fatalf("retryIn = %+v, want 1000", first.RetryIn)
			}
			virtual.advance(600)
			rhHold(peer, 1, rhFlaky)
			second := peer.Next("failed").(*link.FailedMessage)
			if second.Hold != 1 || second.RetryIn.Value != 400 {
				t.Fatalf("second = %+v, want hold 1 retryIn 400", second)
			}
			virtual.advance(400)
			// The attempts run on tasks of the host's loop: wait until both failures arrived.
			rhWaitFor(t, func() bool { return len(peer.Kinds()) >= 5 })
			rhKindsAre(t, peer, "hello", "loading", "loading", "failed", "failed")
			if got := peer.Next("failed").(*link.FailedMessage).RetryIn.Value; got != 2000 {
				t.Fatalf("retryIn = %d, want 2000", got)
			}
			virtual.advance(2000)
			peer.Next("bound")
			peer.Next("bound")
			rhSync(t, peer)
			rhKindsAre(t, peer, "hello", "loading", "loading", "failed", "loading", "loading")
		})

		// hold.test.ts: notices of a failed object › a retry from one consumer renews a broken object for every holder: loading, then bound (races 12, 13)
		t.Run("a retry from one consumer renews a broken object for every holder: loading, then bound (races 12, 13)", func(t *testing.T) {
			virtual := newRhVirtual()
			server, peer, _ := virtual.rawServer(t, rhServeFlaky(&rhFailing{left: 1, broken: true}))
			other := rhSecondPeer(t, server)
			rhHold(peer, 0, rhFlaky)
			rhHold(other, 0, rhFlaky)
			for _, consumer := range []*RawPeer{peer, other} {
				failed := rhOfflineFailed(t, consumer.Next("failed"), 0, "broken")
				if failed.RetryIn.Ok {
					t.Fatalf("a broken failure names a retryIn: %+v", failed)
				}
			}
			peer.Send(&link.RetryMessage{Hold: 0})
			for _, consumer := range []*RawPeer{peer, other} {
				if loading := consumer.Next("loading").(*link.LoadingMessage); loading.Hold != 0 {
					t.Fatalf("loading = %+v", loading)
				}
				if bound := consumer.Next("bound").(*link.BoundMessage); bound.Holds[0].Hold != 0 {
					t.Fatalf("bound = %+v", bound)
				}
			}
		})

		// hold.test.ts: notices of a failed object › a retry skips a transient failure's window for every holder (race 12)
		t.Run("a retry skips a transient failure's window for every holder (race 12)", func(t *testing.T) {
			virtual := newRhVirtual()
			server, peer, _ := virtual.rawServer(t, rhServeFlaky(&rhFailing{left: 1}))
			other := rhSecondPeer(t, server)
			rhHold(peer, 0, rhFlaky)
			peer.Next("failed")
			rhHold(other, 0, rhFlaky)
			other.Next("failed")
			other.Send(&link.RetryMessage{Hold: 0})
			for _, consumer := range []*RawPeer{peer, other} {
				consumer.Next("loading")
				consumer.Next("bound")
			}
		})

		// hold.test.ts: notices of a failed object › a retry of a live or an unknown hold does nothing
		t.Run("a retry of a live or an unknown hold does nothing", func(t *testing.T) {
			virtual := newRhVirtual()
			_, peer, _ := virtual.rawServer(t, rhServeFlaky(&rhFailing{left: 0}))
			rhHold(peer, 0, rhFlaky)
			peer.Next("bound")
			peer.Send(&link.RetryMessage{Hold: 0})
			peer.Send(&link.RetryMessage{Hold: 9})
			rhSync(t, peer)
			rhKindsAre(t, peer, "hello")
			if peer.Closed() {
				t.Fatal("the link closed")
			}
		})
	})
}

// --- holds that bind to nothing yet ----------------------------------------------------------------------------------

func rhActivityReferences(t testing.TB, host Host) float64 {
	t.Helper()
	var references float64
	host.(*chordHost).loop().Run(func() {
		value, err := host.Activity().Value()
		must(t, err)
		references = value.(*delta.JsonObject).Value("references").(float64)
	})
	return references
}

func TestHoldsThatBindToNothingYet(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: holds that bind to nothing yet (dependency model §1, §3) › a refused hold waits failed { waiting }, counted; a retry re-runs its start (loading, failed); its link's close drops it
		t.Run("a refused hold waits failed { waiting }, counted; a retry re-runs its start (loading, failed); its link's close drops it", func(t *testing.T) {
			rig := rawLinked(t, func(FacetHost) error { return nil }, rawLinkedOptions{})
			peer := rig.Peer
			peer.Send(&link.HoldMessage{
				Hold: 0, Type: "app.absent", Version: 1, Fingerprint: "0123456789abcdef", Params: link.Text("{}"), Address: serverWireAddress,
			})
			failed := peer.Next("failed").(*link.FailedMessage)
			if failed.Hold != 0 || failed.Cause.Kind != "waiting" || failed.RetryIn.Ok {
				t.Fatalf("failed = %+v", failed)
			}
			if got := rhActivityReferences(t, rig.Server); got != 1 {
				t.Fatalf("references = %v, want 1", got)
			}
			refs, err := rig.Server.References(rhHalves).Value()
			must(t, err)
			rhEqual(t, "references(Halves)", refs, []any{})
			peer.Send(&link.RetryMessage{Hold: 0})
			if loading := peer.Next("loading").(*link.LoadingMessage); loading.Hold != 0 {
				t.Fatalf("loading = %+v", loading)
			}
			if again := peer.Next("failed").(*link.FailedMessage); again.Hold != 0 || again.Cause.Kind != "waiting" {
				t.Fatalf("failed = %+v", again)
			}
			peer.Close()
			rhWaitFor(t, func() bool { return rhActivityReferences(t, rig.Server) == 0 })
		})

		// hold.test.ts: holds that bind to nothing yet (dependency model §1, §3) › a hold on an object parked on a failed dependency is told each new cause, then loading, then bound
		t.Run("a hold on an object parked on a failed dependency is told each new cause, then loading, then bound", func(t *testing.T) {
			core := mustContract(contracts.Local("app.core", contracts.LocalDeclaration{Params: contracts.Params{},
				Version: 1, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
			}))
			var mu sync.Mutex
			cores := 0
			rig := rawLinked(t, func(host FacetHost) error {
				if err := host.Serve(core, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
					mu.Lock()
					cores++
					n := cores
					mu.Unlock()
					if n < 3 {
						return contracts.Implementation{}, fmt.Errorf("bug %d", n)
					}
					return contracts.Implementation{Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{"n": rhConst(float64(n))},
					}}, nil
				}, ServeOptions{}); err != nil {
					return err
				}
				return host.Serve(rhHalves, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
					coreRef := self.Get(core, jsonObject())
					n := stateOf(self, rhCounter)
					return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
						State: map[string]chord.Watchable[delta.JsonValue]{
							"n": n.Member(),
							"half": self.Derive([]any{coreRef.Member("n")}, func(values []any) (delta.JsonValue, error) {
								return values[0], nil
							}),
						},
						Methods: map[string]contracts.MethodFunction{
							"set":  func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
							"wait": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
						},
					}}, nil
				}, ServeOptions{})
			}, rawLinkedOptions{})
			peer := rig.Peer
			rhHold(peer, 0, rhHalves)
			waiting := func(message string) string {
				return fmt.Sprintf(`{"kind":"chord","error":{"code":"unavailable","message":%q}}`, message)
			}
			check := func(message link.Message, text string) {
				t.Helper()
				failed := message.(*link.FailedMessage)
				if failed.Hold != 0 || failed.Cause.Kind != "waiting" || rhWire(t, failed.Cause.Error) != waiting(text) {
					t.Fatalf("failed = hold %d %s %s, want waiting %s", failed.Hold, failed.Cause.Kind, rhWire(t, failed.Cause.Error), text)
				}
			}
			// The core failed at load (its first outcome): the parked object names it at once.
			check(peer.Next("failed"), "waiting for app.core: app.core: bug 1")
			// A retry passes on through the parked object to its broken dependency (one host's retry operation). (Its new
			// attempt failed within the batch of changes the parked object checks once: no loading between.)
			peer.Send(&link.RetryMessage{Hold: 0})
			check(peer.Next("failed"), "waiting for app.core: app.core: bug 2")
			peer.Send(&link.RetryMessage{Hold: 0})
			peer.Next("loading")
			if bound := peer.Next("bound").(*link.BoundMessage); bound.Holds[0].Hold != 0 {
				t.Fatalf("bound = %+v", bound)
			}
		})
	})
}

// rhWaitFor polls cond, which reads the host through its own sections: the test has nothing else to wait on (the effect
// is a host's reaction to a link closing on the other end).
func rhWaitFor(t testing.TB, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		runtime.Gosched()
	}
}

// --- a reconnecting consumer -----------------------------------------------------------------------------------------

var (
	rhTallyState = []contracts.Property{rhProp("n", contracts.Obj(contracts.Prop("n", contracts.Int())))}
	rhTally      = rhRemote("app.tally", rhTallyState, contracts.Meth("bump", contracts.NewMethod()))
	rhFresh      = rhRemote("app.fresh", rhTallyState, contracts.Meth("bump", contracts.NewMethod()))
)

// rhServeTally serves `Tally` (lingering a minute) and `Fresh` (not lingering, its native's close waits for closing).
func rhServeTally(closing <-chan struct{}) func(FacetHost) error {
	serve := func(self Self) contracts.Implementation {
		doc := stateOf(self, rhCounter)
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"n": doc.Member()},
			Methods: map[string]contracts.MethodFunction{
				"bump": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("n", draft.Get("n").(float64)+1) }, ctx)
				},
			},
		}}
	}
	return func(host FacetHost) error {
		linger := time.Minute
		if err := host.Serve(rhTally, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			return serve(self), nil
		}, ServeOptions{Linger: &linger}); err != nil {
			return err
		}
		return host.Serve(rhFresh, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			self.Open(func(context.Context) (any, error) { return nil, nil }, func(any, NativeCloseReason, context.Context) error {
				<-closing
				return nil
			})
			return serve(self), nil
		}, ServeOptions{})
	}
}

// rhReconnect drops the client's link and connects it again.
func rhReconnect(t testing.TB, hosts *linkedHosts) {
	t.Helper()
	hosts.Links[0].Close()
	transported(hosts.Links[0], hosts.Links[1])
	rhParentDown(t, hosts.Client)
	a, b := link.MemoryLinkPair()
	must(t, hosts.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, hosts.Client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	transported(a, b)
}

func rhRefKind(ref *Reference) RefStateKind { return rhRefState(ref) }

func rhNumber(t testing.TB, ref *Reference, member string) float64 {
	t.Helper()
	value, err := ref.Member(member).Value()
	must(t, err)
	return value.(*delta.JsonObject).Value("n").(float64)
}

func TestHoldAReconnectingConsumer(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: a reconnecting consumer › finds a lingering object as it left it: its in-memory documents kept; one without linger is new (race 7)
		t.Run("finds a lingering object as it left it: its in-memory documents kept; one without linger is new (race 7)", func(t *testing.T) {
			closing := make(chan struct{})
			close(closing)
			hosts := linked(t, rhServeTally(closing), linkedOptions{})
			tally := exGet(t, hosts.Client, rhTally, jsonObject())
			fresh := exGet(t, hosts.Client, rhFresh, jsonObject())
			_, err := tally.Call("bump", nil, background)
			must(t, err)
			_, err = fresh.Call("bump", nil, background)
			must(t, err)
			rhReconnect(t, hosts)
			rhWaitFor(t, func() bool { return rhRefKind(tally) == RefStateLive && rhRefKind(fresh) == RefStateLive })
			if got := []float64{rhNumber(t, tally, "n"), rhNumber(t, fresh, "n")}; got[0] != 1 || got[1] != 0 {
				t.Fatalf("n = %v, want [1 0]", got)
			}
		})

		// hold.test.ts: a reconnecting consumer › re-holds an object whose previous instance still tears down: bound once that ended (race 17)
		t.Run("re-holds an object whose previous instance still tears down: bound once that ended (race 17)", func(t *testing.T) {
			closing := make(chan struct{})
			hosts := linked(t, rhServeTally(closing), linkedOptions{})
			fresh := exGet(t, hosts.Client, rhFresh, jsonObject())
			_, err := fresh.Call("bump", nil, background)
			must(t, err)
			rhReconnect(t, hosts)
			if got, n := rhRefKind(fresh), rhNumber(t, fresh, "n"); got != RefStateLoading || n != 1 {
				t.Fatalf("state, n = %v, %v; want loading, 1", got, n)
			}
			close(closing)
			rhWaitFor(t, func() bool { return rhRefKind(fresh) == RefStateLive })
			if n := rhNumber(t, fresh, "n"); n != 0 {
				t.Fatalf("n = %v, want 0", n)
			}
		})
	})
}

// --- retired members and a full window -------------------------------------------------------------------------------

// rhRetiring is a remote type whose document can be retired; `big` derives a flag that a retirement of a small value
// leaves as is, `t` is `n` and throws while it is negative.
var rhRetiring = rhRemote("app.retiring",
	[]contracts.Property{rhProp("doc", contracts.Obj(contracts.Prop("n", contracts.Int()))), rhProp("big", contracts.Bool()), rhProp("t", contracts.Int())},
	contracts.Meth("set", contracts.NewMethod(contracts.Int())), contracts.Meth("retire", contracts.NewMethod()))

type rhRetiringRig struct {
	*RawPeer
	t     testing.TB
	names wireIndexes
	calls int
}

// rhRetiringPeer is a server serving rhRetiring to a raw peer that acks only when told.
func rhRetiringPeer(t testing.TB) *rhRetiringRig {
	t.Helper()
	server := rhCreate(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(rhRetiring, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, rhCounter)
			number := func(values []any) float64 { return values[0].(*delta.JsonObject).Value("n").(float64) }
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"doc": doc.Member(),
					"big": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) { return number(values) > 10, nil }),
					"t": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						if n := number(values); n < 0 {
							return nil, fmt.Errorf("failed %d", int(n))
						}
						return number(values), nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
					},
					"retire": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) { return nil, doc.Retire(ctx) },
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	peer := rhSecondPeerWith(t, server, rawPeerOptions{NoAck: true})
	rhSync(t, peer)
	rig := &rhRetiringRig{RawPeer: peer, t: t, names: wireNames(t, rhRetiring, "v")}
	rhHold(peer, 0, rhRetiring)
	peer.Next("bound")
	peer.Ack(link.Optional[int]{})
	return rig
}

func rhSecondPeerWith(t testing.TB, server Host, options rawPeerOptions) *RawPeer {
	t.Helper()
	a, b := link.MemoryLinkPair()
	must(t, server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	return newRawPeer(t, b, options)
}

// call calls a method of hold 0 and waits for its result.
func (r *rhRetiringRig) call(method string, args ...int) {
	r.t.Helper()
	id := r.calls
	r.calls++
	message := &link.CallMessage{Call: id, Hold: 0, Method: r.names.Methods[method]}
	if len(args) > 0 {
		part, err := link.JsonPart(float64(args[0]))
		must(r.t, err)
		message.Args = part
	}
	r.Send(message)
	if got := r.Next("result").(*link.ResultMessage).Call; got != id {
		r.t.Fatalf("result for call %d, want %d", got, id)
	}
}

func (r *rhRetiringRig) nextState() link.StateEntry {
	r.t.Helper()
	return r.Next("state").(*link.StateMessage).Holds[0]
}

func rhSorted(list []int) []int {
	sorted := slices.Clone(list)
	slices.Sort(sorted)
	return sorted
}

func TestHoldRetiredMembers(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: retired members (decision "One retirement mechanism") › are listed in the state that carries the retirement, a derive whose value stayed without ops
		t.Run("are listed in the state that carries the retirement, a derive whose value stayed without ops", func(t *testing.T) {
			rig := rhRetiringPeer(t)
			retiredAll := rhSorted([]int{rig.names.State["doc"], rig.names.State["big"], rig.names.State["t"]})
			rig.call("set", 1)
			if set := rig.nextState(); len(set.Retired) != 0 {
				t.Fatalf("retired = %v, want none", set.Retired)
			}
			rig.Ack(link.Optional[int]{})
			rig.call("retire")
			retired := rig.nextState()
			rhEqual(t, "members", rhNamedState(t, rig.names, retired), map[string]any{
				"doc": []any{[]any{"r", map[string]any{"n": 0}}}, "t": []any{[]any{"r", 0}},
			})
			rhEqual(t, "retired", rhSorted(retired.Retired), retiredAll)
		})

		// hold.test.ts: retired members (decision "One retirement mechanism") › are kept while the window is full and sent once with the folded ops
		t.Run("are kept while the window is full and sent once with the folded ops", func(t *testing.T) {
			rig := rhRetiringPeer(t)
			retiredAll := rhSorted([]int{rig.names.State["doc"], rig.names.State["big"], rig.names.State["t"]})
			rig.call("set", 1)
			rig.call("set", 2)
			rig.Next("state")
			rig.Next("state")
			// The window is full: the retirement and the next change wait, folded into one entry.
			rig.call("retire")
			rig.call("set", 4)
			rhSync(t, rig.RawPeer)
			if slices.Contains(rig.Kinds(), "state") {
				t.Fatalf("a state was sent while the window is full: %v", rig.Kinds())
			}
			rig.Ack(link.Optional[int]{})
			pending := rig.nextState()
			rhEqual(t, "members", rhNamedState(t, rig.names, pending), map[string]any{
				"doc": []any{[]any{"r", map[string]any{"n": 0}}, []any{"s", []any{"n"}, 4}},
				"t":   []any{[]any{"r", 4}},
			})
			rhEqual(t, "retired", rhSorted(pending.Retired), retiredAll)
			rig.Ack(link.Optional[int]{})
			rig.Ack(link.Optional[int]{})
			rig.call("set", 5)
			if next := rig.nextState(); len(next.Retired) != 0 {
				t.Fatalf("retired = %v, want none", next.Retired)
			}
		})

		// hold.test.ts: retired members (decision "One retirement mechanism") › of a member failed again before its entry leaves wait for its next value, never with its failure
		t.Run("of a member failed again before its entry leaves wait for its next value, never with its failure", func(t *testing.T) {
			rig := rhRetiringPeer(t)
			rig.call("set", 1)
			rig.call("set", 2)
			rig.Next("state")
			rig.Next("state")
			// The window is full: `t` fails, heals with the retirement (n is 0 again), and fails again.
			rig.call("set", -1)
			rig.call("retire")
			rig.call("set", -3)
			rig.Ack(link.Optional[int]{})
			failed := rig.nextState()
			if got, want := rhFailed(t, failed.Failed), rhPluginFailure(rig.names.State["t"], "failed -3"); got != want {
				t.Fatalf("failed = %s, want %s", got, want)
			}
			if keys := rhKeys(rhNamedState(t, rig.names, failed)); !slices.Equal(keys, []string{"doc"}) {
				t.Fatalf("members = %v, want [doc]", keys)
			}
			rhEqual(t, "retired", rhSorted(failed.Retired), rhSorted([]int{rig.names.State["doc"], rig.names.State["big"]}))
			rig.Ack(link.Optional[int]{})
			rig.Ack(link.Optional[int]{})
			rig.call("set", 5)
			healed := rig.nextState()
			rhEqual(t, "t", rhNamedState(t, rig.names, healed)["t"], []any{[]any{"r", 5}})
			rhEqual(t, "retired", healed.Retired, []int{rig.names.State["t"]})
		})
	})
}

func rhKeys(members map[string]any) []string {
	keys := make([]string, 0, len(members))
	for key := range members {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func TestHoldFailuresWhileTheWindowIsFull(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// hold.test.ts: failures while the window is full › fold per member: 1000 failing commits leave one failure entry, the member's ops dropped; bounded
		t.Run("fold per member: 1000 failing commits leave one failure entry, the member's ops dropped; bounded", func(t *testing.T) {
			rig := rhRetiringPeer(t)
			rig.call("set", 1)
			rig.call("set", 2)
			rig.Next("state")
			rig.Next("state")
			// An earlier episode, healed: its failure leaves with the heal.
			rig.call("set", -1)
			rig.call("set", 3)
			for n := 1; n <= 1000; n++ {
				rig.call("set", -1000-n)
			}
			rig.Ack(link.Optional[int]{})
			pending := rig.nextState()
			// One failure call per episode: the error of the commit that started it.
			if got, want := rhFailed(t, pending.Failed), rhPluginFailure(rig.names.State["t"], "failed -1001"); got != want {
				t.Fatalf("failed = %s, want %s", got, want)
			}
			if _, has := rhNamedState(t, rig.names, pending)["t"]; has {
				t.Fatal("the failed member has ops")
			}
			encoded, err := rig.Codec.Encode(&link.StateMessage{Holds: []link.StateEntry{pending}})
			must(t, err)
			if len(encoded) >= 200 {
				t.Fatalf("state is %d bytes, want < 200", len(encoded))
			}
		})

		// hold.test.ts: failures while the window is full › are replaced by a value that follows them: the value as a root replacement, no failure
		t.Run("are replaced by a value that follows them: the value as a root replacement, no failure", func(t *testing.T) {
			rig := rhRetiringPeer(t)
			rig.call("set", 1)
			rig.call("set", 2)
			rig.Next("state")
			rig.Next("state")
			rig.call("set", -1)
			rig.call("set", 3)
			rig.Ack(link.Optional[int]{})
			pending := rig.nextState()
			if len(pending.Failed) != 0 {
				t.Fatalf("failed = %v, want none", pending.Failed)
			}
			rhEqual(t, "t", rhNamedState(t, rig.names, pending)["t"], []any{[]any{"r", 3}})
		})
	})
}

// --- retry operations across links -----------------------------------------------------------------------------------

// rhCountingLink counts the `retry` messages sent through it (Pi wraps the link and decodes with the binary codec).
type rhCountingLink struct {
	link.Link
	retries *rhRetryCount
}

type rhRetryCount struct {
	mu sync.Mutex
	n  int
}

func (c *rhRetryCount) get() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (l *rhCountingLink) Send(bytes []byte) {
	if message, err := link.BinaryCodec.Decode(bytes); err == nil && message.Kind() == "retry" {
		l.retries.mu.Lock()
		l.retries.n++
		l.retries.mu.Unlock()
	}
	l.Link.Send(bytes)
}

// hold.test.ts: retry operations across links (fix rounds 1 and 2, Sol 3) › quiesce after %i concurrent Retries round a
// waiting ring across hosts: each stops after one lap
func TestHoldRetryOperationsQuiesceAcrossHosts(t *testing.T) {
	for _, operations := range []int{1, 9, 50} {
		t.Run(fmt.Sprintf("quiesce after %d concurrent Retries round a waiting ring across hosts: each stops after one lap", operations), func(t *testing.T) {
			serverKind := mustKind(contracts.NewHostKind("app.retry-server"))
			workerKind := mustKind(contracts.NewHostKind("app.retry-worker"))
			local := func(name string) *contracts.Contract {
				return mustContract(contracts.Local(name, contracts.LocalDeclaration{
					Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
				}))
			}
			reader := func(name string, kind *contracts.HostKind) *contracts.Contract {
				return mustContract(contracts.Remote(name, contracts.RemoteDeclaration{
					Version:     1,
					Params:      contracts.Params{},
					HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return kind.Address(nil), nil },
					Variants: []contracts.VariantEntry{contracts.Var("reader", contracts.VariantDeclaration{
						State: []contracts.Property{rhProp("n", contracts.Int())},
					})},
				}))
			}
			localA, localB := local("app.a"), local("app.b")
			remoteA, remoteB := reader("app.remote-a", serverKind), reader("app.remote-b", workerKind)
			missing := local("app.missing")
			retries := &rhRetryCount{}
			down, up := link.MemoryLinkPair()
			server := rhCreate(t, HostOptions{Address: new(serverKind.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
			worker := rhCreate(t, HostOptions{Address: new(workerKind.Address(nil)), Principal: workerPrincipal("1"), OnError: func(HostError) {}})
			must(t, server.Route(workerKind, RouteOptions{
				Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
				Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
					return ResolvedLink{Link: &rhCountingLink{Link: down, retries: retries}, Principal: workerPrincipal("1"), Trusted: true, Codec: testCodec()}, nil
				},
			}, background))
			must(t, worker.Connect(&rhCountingLink{Link: up, retries: retries}, LinkOptions{Principal: serverPrincipal(), Trusted: true, Codec: testCodec()}, background))
			readerImpl := func(member chord.Watchable[delta.JsonValue]) contracts.Implementation {
				return contracts.Implementation{Variant: "reader", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": member},
				}}
			}
			localImpl := func(member chord.Watchable[delta.JsonValue]) contracts.Implementation {
				return contracts.Implementation{Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": member},
				}}
			}
			var wg sync.WaitGroup
			var leftErr, rightErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				leftErr = server.Load([]HostFacet{{Name: "@test/a", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
					if err := host.Serve(localA, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
						return localImpl(Expose(self.Get(remoteB, jsonObject()).Member("n"))), nil
					}, ServeOptions{}); err != nil {
						return err
					}
					return host.Serve(remoteA, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
						return readerImpl(Expose(self.Get(localA, jsonObject()).Member("n"))), nil
					}, ServeOptions{})
				})}}, background)
			}()
			go func() {
				defer wg.Done()
				rightErr = worker.Load([]HostFacet{{Name: "@test/b", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
					if err := host.Serve(localB, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
						self.Get(missing, jsonObject())
						return localImpl(Expose(self.Get(remoteA, jsonObject()).Member("n"))), nil
					}, ServeOptions{}); err != nil {
						return err
					}
					return host.Serve(remoteB, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
						return readerImpl(Expose(self.Get(localB, jsonObject()).Member("n"))), nil
					}, ServeOptions{})
				})}}, background)
			}()
			wg.Wait()
			must(t, leftErr)
			must(t, rightErr)
			defer func() {
				must(t, server.Close(background))
				must(t, worker.Close(background))
			}()
			rhSettleLinks(down, up)
			a, err := server.Hold(localA, jsonObject(), background, GetOptions{})
			must(t, err)
			rhWaitFor(t, func() bool { return rhRefState(a) == RefStateFailed })
			if state := rhState(a); state.Kind != RefStateFailed || state.Cause.Kind != data.FailureWaiting {
				t.Fatalf("state = %+v, want failed waiting", state)
			}
			// More at once than an object remembers ids: `through` stops each at the object it passed through already.
			for range operations {
				a.Retry()
			}
			rhSettleLinks(down, up)
			// Each went round once: server → worker → server, then stopped at the object it started from.
			if got := retries.get(); got != 2*operations {
				t.Fatalf("retries = %d, want %d", got, 2*operations)
			}
			rhSettleLinks(down, up)
			if got := retries.get(); got != 2*operations {
				t.Fatalf("retries = %d, want %d", got, 2*operations)
			}
		})
	}
}

// rhSettleLinks is Pi's `flush()` over memory links: every message in flight arrived and was handled.
func rhSettleLinks(links ...*link.MemoryLink) {
	for range 4 {
		transported(links...)
	}
}
