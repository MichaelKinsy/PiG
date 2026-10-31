package host

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/next/host/revocation.test.ts for the part that needs no host: `revocation keys`. Upstream runs
// its two cases through createHost (host.revoke, a RawPeer of hold messages), which belongs to the host core; these are own
// tests of revocationKeys with the behavior each of them asserts: the keys are computed once per principal object and
// compared as canonical JSON ("are computed once per principal object, compared as canonical JSON"), and the cache keeps
// no principal alive ("keep no principal alive (a WeakMap keyed by the principal the host cached them for)"). The
// remaining cases (revoke on one host, links, GC of revoked objects, relays, own requests over paths) are the host's.

func revocationGuest(session string) data.Principal {
	return delta.JsonObjectOf("kind", "guest", "session", session)
}

func revocationCanonical(t *testing.T, value delta.JsonValue) string {
	t.Helper()
	return data.CanonicalJson(value, chordctx.Background())
}

// Own test mirroring "are computed once per principal object, compared as canonical JSON": two principals revoked twice
// each call revokedBy twice in all, and a revocation spelled with another key order is the same key.
func TestRevocationKeysAreComputedOncePerPrincipalObjectComparedAsCanonicalJSON(t *testing.T) {
	calls := 0
	keys := newRevocationKeys(RevokedBy[delta.JsonValue](func(data.Principal) ([]delta.JsonValue, error) {
		calls++
		return []delta.JsonValue{delta.JsonObjectOf("b", 2.0, "a", 1.0)}, nil
	}).erased())
	first, second := revocationGuest("S1"), revocationGuest("S2")
	ctx := chordctx.Background()
	match := revocationCanonical(t, delta.JsonObjectOf("a", 1.0, "b", 2.0))
	miss := revocationCanonical(t, delta.JsonObjectOf("a", 1.0, "b", 3.0))
	for _, principal := range []data.Principal{first, second} {
		if revoked, err := keys.revokes(principal, match, ctx); err != nil || !revoked {
			t.Fatalf("revokes(%v, match) = %v, %v; want true, nil", principal, revoked, err)
		}
		if revoked, err := keys.revokes(principal, miss, ctx); err != nil || revoked {
			t.Fatalf("revokes(%v, miss) = %v, %v; want false, nil", principal, revoked, err)
		}
	}
	if calls != 2 {
		t.Fatalf("revokedBy calls = %d, want 2", calls)
	}
}

// Own test mirroring "keep no principal alive": the cache holds a principal weakly, and goes on working for the others.
func TestRevocationKeysKeepNoPrincipalAlive(t *testing.T) {
	keys := newRevocationKeys(RevokedBy[delta.JsonValue](func(principal data.Principal) ([]delta.JsonValue, error) {
		return []delta.JsonValue{delta.JsonObjectOf("session", principal.Value("session"))}, nil
	}).erased())
	key := revocationCanonical(t, delta.JsonObjectOf("session", "S9"))
	cached := func() weak.Pointer[delta.JsonObject] {
		principal := revocationGuest("S9")
		if revoked, err := keys.revokes(principal, key, chordctx.Background()); err != nil || !revoked {
			t.Fatalf("revokes = %v, %v; want true, nil", revoked, err)
		}
		return weak.Make(principal)
	}()
	nexttest.ExpectCollected(t, cached)
	// The cache's owner is still alive and working.
	other := revocationGuest("S9")
	if revoked, err := keys.revokes(other, key, chordctx.Background()); err != nil || !revoked {
		t.Fatalf("revokes after the collection = %v, %v; want true, nil", revoked, err)
	}
	// Its entry went with the principal: only the live one is left.
	var entries int
	for range 10 {
		runtime.GC()
		keys.keys.mu.Lock()
		entries = len(keys.keys.entries)
		keys.keys.mu.Unlock()
		if entries == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if entries != 1 {
		t.Fatalf("cached principals = %d, want 1", entries)
	}
	runtime.KeepAlive(other)
}

// Own test of what Pi's `revokes` does when `revokedBy` throws: the throw reaches the caller and nothing is cached, so the
// next call asks `revokedBy` again.
func TestRevocationKeysReturnTheErrorOfRevokedByAndCacheNothing(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	keys := newRevocationKeys(func(data.Principal) ([]delta.JsonValue, error) {
		calls++
		if calls == 1 {
			return nil, boom
		}
		return []delta.JsonValue{"x"}, nil
	})
	principal := revocationGuest("S1")
	if _, err := keys.revokes(principal, `"x"`, chordctx.Background()); !errors.Is(err, boom) {
		t.Fatalf("first revokes error = %v, want boom", err)
	}
	if revoked, err := keys.revokes(principal, `"x"`, chordctx.Background()); err != nil || !revoked {
		t.Fatalf("second revokes = %v, %v; want true, nil", revoked, err)
	}
	if calls != 2 {
		t.Fatalf("revokedBy calls = %d, want 2", calls)
	}
}

// Own test of `revokedBy === undefined`: nothing revokes, and an absent function stays absent through the host's cast.
func TestRevocationKeysWithoutRevokedByRevokeNothing(t *testing.T) {
	var absent RevokedBy[delta.JsonValue]
	if absent.erased() != nil {
		t.Fatal("an absent revokedBy erased is not nil")
	}
	keys := newRevocationKeys(absent.erased())
	if revoked, err := keys.revokes(revocationGuest("S1"), `"x"`, chordctx.Background()); err != nil || revoked {
		t.Fatalf("revokes = %v, %v; want false, nil", revoked, err)
	}
}

// Own test of the cast: the typed revocations become JSON values in order.
func TestRevokedByErasedKeepsTheOrderOfTheRevocations(t *testing.T) {
	erased := RevokedBy[string](func(data.Principal) ([]string, error) { return []string{"a", "b"}, nil }).erased()
	values, err := erased(revocationGuest("S1"))
	if err != nil || !reflect.DeepEqual(values, []delta.JsonValue{"a", "b"}) {
		t.Fatalf("erased = %v, %v; want [a b], nil", values, err)
	}
}

// --- upstream revocation.test.ts cases that run on hosts (createHost, host.revoke, links, RawPeer, relays) -----------

// revokedByApp is the test app's revocations: a guest's or worker's session, or every owner (`revokedBy` of the file).
func revokedByApp(principal data.Principal) ([]delta.JsonValue, error) {
	switch principalKind(principal) {
	case "guest", "worker":
		return []delta.JsonValue{jsonObject("session", principal.Value("session"))}, nil
	case "owner":
		return []delta.JsonValue{jsonObject("owners", true)}, nil
	}
	return nil, nil
}

// revocationS1 is the file's `S1`.
var revocationS1 = guestPrincipal("S1")

// barrierType is a host-lifetime type whose activation waits for an open: `load` waits for it (the start barrier).
var barrierType = mustContract(declareLocal("app.barrier", contracts.LocalDeclaration{Params: contracts.Params{},
	Version: 1,
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

// revocationWorker is a worker of session S1 serving Notes (as serveNotes, at the address Notes names), its parent a raw peer
// whose holds name their principal; the links' ends are returned for transported.
func revocationWorker(t *testing.T, setup func(host FacetHost) error) (Host, *RawPeer, [2]*link.MemoryLink, *notesProbe) {
	t.Helper()
	address := serverAddress()
	host := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal("S1"), RevokedBy: revokedByApp, OnError: func(HostError) {}})
	var probe *notesProbe
	if setup == nil {
		setup = func(h FacetHost) error {
			var err error
			probe, err = serveNotes(h, nil)
			return err
		}
	}
	must(t, host.Load([]HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { return setup(h) })}}, background))
	parent, ends := rawParentLinks(t, host)
	return host, parent, ends, probe
}

// rawParentLinks is rawParent with the two ends of the link, for transported.
func rawParentLinks(t testing.TB, host Host) (*RawPeer, [2]*link.MemoryLink) {
	t.Helper()
	a, b := link.MemoryLinkPair()
	must(t, host.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	peer.Next("hello")
	return peer, [2]*link.MemoryLink{a, b}
}

// holdAs sends a hold of typ for S1 with id hold, acting as principal (honoured: the peer is the parent).
func holdAs(t testing.TB, peer *RawPeer, hold int, principal data.Principal, typ *contracts.Contract) {
	t.Helper()
	params, err := link.JsonPart(jsonObject("session", "S1"))
	must(t, err)
	peer.Send(&link.HoldMessage{
		Hold: hold, Type: typ.Name, Version: typ.Version, Fingerprint: typ.Fingerprint,
		Params: params, Address: serverWireAddress, Principal: principal,
	})
}

// revoked is the wire's `{ kind: "chord", error: { code: "forbidden", reason: "revoked", message } }`.
func revokedWire(message string) *delta.JsonObject {
	return jsonObject("kind", "chord", "error", jsonObject("code", "forbidden", "reason", "revoked", "message", message))
}

// goneOf is the next `gone` message of the peer.
func goneOf(peer *RawPeer) *link.GoneMessage { return peer.Next("gone").(*link.GoneMessage) }

// Mirrors revocation.test.ts "revocation keys › are computed once per principal object, compared as canonical JSON".
func TestHostRevocationKeysAreComputedOncePerPrincipalObjectComparedAsCanonicalJSON(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	address := serverAddress()
	host := newHost(t, HostOptions{
		Address: &address, Principal: serverPrincipal(),
		RevokedBy: func(data.Principal) ([]delta.JsonValue, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			return []delta.JsonValue{jsonObject("b", float64(2), "a", float64(1))}, nil
		},
		OnError: func(HostError) {},
	})
	defer closeHosts(t, host)
	must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, nil); return err })}}, background))
	peer, ends := rawParentLinks(t, host)
	holdAs(t, peer, 0, revocationS1, Notes)
	holdAs(t, peer, 1, guestPrincipal("S2"), Notes)
	peer.Next("bound")
	peer.Next("bound")
	must(t, host.Revoke(jsonObject("a", float64(1), "b", float64(2)), background))
	must(t, host.Revoke(jsonObject("a", float64(1), "b", float64(3)), background))
	transported(ends[0], ends[1])
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 2 {
		t.Fatalf("revokedBy calls = %d, want 2", got)
	}
	if hold := goneOf(peer).Hold; hold != 0 {
		t.Fatalf("first gone hold = %d, want 0", hold)
	}
	if hold := goneOf(peer).Hold; hold != 1 {
		t.Fatalf("second gone hold = %d, want 1", hold)
	}
}

// Mirrors "revocation keys › keep no principal alive (a WeakMap keyed by the principal the host cached them for)".
func TestHostRevocationKeysKeepNoPrincipalAlive(t *testing.T) {
	var mu sync.Mutex
	var cached []weak.Pointer[delta.JsonObject]
	address := serverAddress()
	host := newHost(t, HostOptions{
		Address: &address, Principal: workerPrincipal("S1"),
		RevokedBy: func(principal data.Principal) ([]delta.JsonValue, error) {
			// The principal decoded on this host, the cache's key: not the sender's object.
			mu.Lock()
			cached = append(cached, weak.Make(principal))
			mu.Unlock()
			return revokedByApp(principal)
		},
		OnError: func(HostError) {},
	})
	defer closeHosts(t, host)
	must(t, host.Load([]HostFacet{{Name: "@test/worker", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, nil); return err })}}, background))
	parent, ends := rawParentLinks(t, host)
	holdAs(t, parent, 0, guestPrincipal("S9"), Notes)
	parent.Next("bound")
	must(t, host.Revoke(jsonObject("session", "S1"), background))
	parent.Send(&link.ReleaseMessage{Hold: 0})
	transported(ends[0], ends[1])
	mu.Lock()
	refs := append([]weak.Pointer[delta.JsonObject](nil), cached...)
	mu.Unlock()
	if len(refs) == 0 {
		t.Fatal("revokedBy was never called")
	}
	nexttest.ExpectCollected(t, refs...)
	// The cache's owner is still alive and working.
	holdAs(t, parent, 1, revocationS1, Notes)
	parent.Next("bound")
	must(t, host.Revoke(jsonObject("session", "S1"), background))
	if hold := goneOf(parent).Hold; hold != 1 {
		t.Fatalf("gone hold = %d, want 1", hold)
	}
}

// Mirrors "revoke on one host › ends the principal's objects at once, whatever references remain: each hold gets gone forbidden".
func TestHostRevokeEndsThePrincipalsObjectsAtOnceWhateverReferencesRemain(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host, parent, ends, _ := revocationWorker(t, nil)
		defer closeHosts(t, host)
		holdAs(t, parent, 0, revocationS1, Notes)
		holdAs(t, parent, 1, revocationS1, Notes)
		holdAs(t, parent, 2, ownerPrincipal(), Notes)
		for range 3 {
			parent.Next("bound")
		}
		must(t, host.Revoke(jsonObject("session", "S1"), background))
		transported(ends[0], ends[1])
		gone := []*link.GoneMessage{goneOf(parent), goneOf(parent)}
		holds := []int{gone[0].Hold, gone[1].Hold}
		slices.Sort(holds)
		if !reflect.DeepEqual(holds, []int{0, 1}) {
			t.Fatalf("gone holds = %v, want [0 1]", holds)
		}
		nexttest.RequireJSONEqual(t, gone[0].Error, revokedWire("app.notes(S1): revoked"))
		// The owner's hold, the parent link and the local store stay.
		if parent.Closed() {
			t.Fatal("the parent link closed")
		}
		if kinds := parent.Kinds(); len(kinds) != 0 {
			t.Fatalf("kinds after the gone = %v, want none", kinds)
		}
		holdAs(t, parent, 3, revocationS1, Notes)
		if hold := parent.Next("bound").(*link.BoundMessage).Holds[0].Hold; hold != 3 {
			t.Fatalf("bound hold = %d, want 3", hold)
		}
	})
}

// Mirrors "revoke on one host › aborts running calls (race 2): the handler's context, no result; a call crossing the gone is ignored".
func TestHostRevokeAbortsRunningCalls(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host, parent, ends, probe := revocationWorker(t, nil)
		defer closeHosts(t, host)
		reader := wireNames(t, Notes, "reader")
		holdAs(t, parent, 0, revocationS1, Notes)
		parent.Next("bound")
		parent.Send(&link.CallMessage{Call: 0, Hold: 0, Method: reader.Methods["wait"]})
		waits := probe.AwaitWaits(t, 1)
		must(t, host.Revoke(jsonObject("session", "S1"), background))
		<-waits[0].Done()
		if chordErr, ok := data.IsChordError(context.Cause(waits[0])); !ok || chordErr.Code != data.CodeForbidden {
			t.Fatalf("the handler's abort reason = %v, want forbidden", context.Cause(waits[0]))
		}
		if hold := goneOf(parent).Hold; hold != 0 {
			t.Fatalf("gone hold = %d, want 0", hold)
		}
		transported(ends[0], ends[1])
		if kinds := parent.Kinds(); len(kinds) != 0 {
			t.Fatalf("kinds after the gone = %v, want none", kinds)
		}
		parent.Send(&link.CallMessage{Call: 1, Hold: 0, Method: reader.Methods["wait"]})
		transported(ends[0], ends[1])
		if kinds := parent.Kinds(); len(kinds) != 0 {
			t.Fatalf("kinds after the call crossing the gone = %v, want none", kinds)
		}
	})
}

// Mirrors "revoke on one host › abandons an activation in progress (race 1): its open's context is aborted, its holders get gone".
func TestHostRevokeAbandonsAnActivationInProgress(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		slow := mustContract(declareRemote("app.slow", contracts.RemoteDeclaration{
			Version:     1,
			HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
			Variants:    []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("n", contracts.Int())}})},
		}))
		var mu sync.Mutex
		var aborted []error
		started := make(chan struct{})
		host, parent, ends, _ := revocationWorker(t, func(h FacetHost) error {
			return h.Serve(slow, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
				selfOpen(self, func(opening context.Context) error {
					close(started)
					<-opening.Done()
					mu.Lock()
					aborted = append(aborted, context.Cause(opening))
					mu.Unlock()
					return context.Cause(opening)
				}, func(NativeCloseReason) {})
				return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
					State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(1))},
				}}, nil
			}, ServeOptions{})
		})
		defer closeHosts(t, host)
		params := link.Text("{}")
		parent.Send(&link.HoldMessage{
			Hold: 0, Type: slow.Name, Version: slow.Version, Fingerprint: slow.Fingerprint,
			Params: params, Address: serverWireAddress, Principal: revocationS1,
		})
		<-started
		transported(ends[0], ends[1])
		must(t, host.Revoke(jsonObject("session", "S1"), background))
		gone := goneOf(parent)
		if text := nexttest.JSONText(t, gone.Error); !strings.Contains(text, `"code":"forbidden"`) {
			t.Fatalf("gone error = %s, want forbidden", text)
		}
		// Pi's listener on the open's abort signal runs when the signal fires, before the `gone` is read; here the open waits for
		// its context on a goroutine of its own, which the revoke wakes without being ordered before the `gone`.
		scopeEventually(t, "the open to see its abort", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(aborted) > 0
		})
		mu.Lock()
		reasons := append([]error(nil), aborted...)
		mu.Unlock()
		if len(reasons) != 1 {
			t.Fatalf("aborted opens = %d, want 1", len(reasons))
		}
		if chordErr, ok := data.IsChordError(reasons[0]); !ok || chordErr.Code != data.CodeForbidden {
			t.Fatalf("open abort reason = %v, want forbidden", reasons[0])
		}
	})
}

// Mirrors "revoke on one host › never revokes the host's own principal".
func TestHostRevokeNeverRevokesTheHostsOwnPrincipal(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host, _, _, _ := revocationWorker(t, nil)
		defer closeHosts(t, host)
		own, err := host.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		must(t, host.Revoke(jsonObject("session", "S1"), background))
		if kind := refStateOf(own).Kind; kind != RefStateLive {
			t.Fatalf("state = %s, want live", kind)
		}
	})
}

// Mirrors "revoke on one host › ends a hold the parent sent before its revoke while it still waits for load (race 3): gone, never bound".
func TestHostRevokeEndsAHoldTheParentSentBeforeItsRevokeWhileItStillWaitsForLoad(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		for _, delayed := range []bool{false, true} {
			t.Run(fmt.Sprintf("delayed=%v", delayed), func(t *testing.T) {
				// A worker connects before it loads (W6); a load can wait for an activation (the start barrier).
				opened := make(chan struct{})
				address := serverAddress()
				host := newHost(t, HostOptions{Address: &address, Principal: workerPrincipal("S1"), RevokedBy: revokedByApp, OnError: func(HostError) {}})
				defer closeHosts(t, host)
				parent, ends, loop := rawParentOnLoop(t, host)
				facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
					if _, err := serveNotes(h, nil); err != nil {
						return err
					}
					if delayed {
						return h.Serve(barrierType, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
							selfOpen(self, func(context.Context) error { <-opened; return nil }, func(NativeCloseReason) {})
							return contracts.Implementation{Members: contracts.VariantImplementation{
								State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(float64(0))},
							}}, nil
						}, ServeOptions{})
					}
					return nil
				})
				loading := make(chan error, 1)
				go func() { loading <- host.Load([]HostFacet{{Name: "@test/worker", Facet: facet}}, background) }()
				// Pi's peer sends both in one turn, over a memory link on the one microtask queue: the hold is delivered, its
				// `bound` flush is queued behind the revoke's delivery, and the revoke ends the hold before any `bound` leaves.
				loop.Run(func() {
					holdAs(t, parent, 0, revocationS1, Notes)
					parent.Send(&link.RevokeMessage{Revocation: jsonObject("session", "S1")})
				})
				transported(ends[0], ends[1])
				close(opened)
				must(t, <-loading)
				transported(ends[0], ends[1])
				gone := goneOf(parent)
				if gone.Hold != 0 {
					t.Fatalf("gone hold = %d, want 0", gone.Hold)
				}
				nexttest.RequireJSONEqual(t, gone.Error.Value("error").(*delta.JsonObject).Value("reason"), "revoked")
				if kinds := parent.Kinds(); len(kinds) != 0 {
					t.Fatalf("kinds = %v, want none: the hold was never bound", kinds)
				}
				if text := nexttest.JSONText(t, sourceValue(t, host, host.References(Notes))); text != "[]" {
					t.Fatalf("presence = %s, want []", text)
				}
			})
		}
	})
}

// Mirrors "revoke on one host › ends a relayed hold of the principal still waiting for its route's link: never held there".
func TestHostRevokeEndsARelayedHoldStillWaitingForItsRoutesLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host := newHost(t, HostOptions{Principal: workerPrincipal("S0"), RevokedBy: revokedByApp, OnError: func(HostError) {}})
		defer closeHosts(t, host)
		resolving := make(chan struct{})
		var mu sync.Mutex
		var routed []*RawPeer
		mustRoute(t)(host.Route(Session, RouteOptions{
			Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
			Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
				<-resolving
				a, b := link.MemoryLinkPair()
				mu.Lock()
				routed = append(routed, newRawPeer(t, b, rawPeerOptions{}))
				mu.Unlock()
				return ResolvedLink{Link: a, Principal: workerPrincipal("S1"), Codec: testCodec()}, nil
			},
		}, background))
		must(t, host.Load(nil, background))
		parent, ends := rawParentLinks(t, host)
		params, err := link.JsonPart(jsonObject("session", "S1"))
		must(t, err)
		parent.Send(&link.HoldMessage{
			Hold: 0, Type: PlacedNotes.Name, Version: PlacedNotes.Version, Fingerprint: PlacedNotes.Fingerprint,
			Params: params, Address: link.WireAddress{Kind: "session", Params: jsonObject("id", "S1")}, Principal: revocationS1,
		})
		transported(ends[0], ends[1])
		parent.Send(&link.RevokeMessage{Revocation: jsonObject("session", "S1")})
		gone := goneOf(parent)
		if gone.Hold != 0 {
			t.Fatalf("gone hold = %d, want 0", gone.Hold)
		}
		nexttest.RequireJSONEqual(t, gone.Error.Value("error").(*delta.JsonObject).Value("reason"), "revoked")
		close(resolving)
		// The resolve runs to its end (its link is made, the route keeps it until it closes) before the test ends.
		scopeEventually(t, "the resolve finished", func() bool { mu.Lock(); defer mu.Unlock(); return len(routed) == 1 })
		transported(ends[0], ends[1])
		transported(ends[0], ends[1])
		mu.Lock()
		defer mu.Unlock()
		for _, peer := range routed {
			for _, message := range peer.Received() {
				if message.Kind() == "hold" {
					t.Fatalf("the route link got a hold: %v", message)
				}
			}
		}
	})
}

// Mirrors "revoke on one host › ends an unbound hold of the principal (refused, waiting for a provider): gone, nothing left of it".
func TestHostRevokeEndsAnUnboundHoldOfThePrincipal(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host, parent, ends, _ := revocationWorker(t, nil)
		defer closeHosts(t, host)
		absent := *Notes
		absent.Name = "app.absent"
		holdAs(t, parent, 0, revocationS1, &absent)
		failed := parent.Next("failed").(*link.FailedMessage)
		if failed.Hold != 0 || failed.Cause.Kind != "waiting" {
			t.Fatalf("failed = %+v, want hold 0 waiting", failed)
		}
		if got := activityOf(host).References; got != 1 {
			t.Fatalf("references = %d, want 1", got)
		}
		parent.Send(&link.RevokeMessage{Revocation: jsonObject("session", "S1")})
		gone := goneOf(parent)
		if gone.Hold != 0 {
			t.Fatalf("gone hold = %d, want 0", gone.Hold)
		}
		nexttest.RequireJSONEqual(t, gone.Error.Value("error").(*delta.JsonObject).Value("reason"), "revoked")
		transported(ends[0], ends[1])
		if got := activityOf(host).References; got != 0 {
			t.Fatalf("references after the revoke = %d, want 0", got)
		}
	})
}

// Mirrors "revoke on one host › binds a hold made for a revoked principal, then the parent's revoke after it ends it (race 3, R10's window)".
func TestHostRevokeBindsAHoldMadeForARevokedPrincipalThenTheParentsRevokeEndsIt(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		host, parent, _, _ := revocationWorker(t, nil)
		defer closeHosts(t, host)
		holdAs(t, parent, 0, revocationS1, Notes)
		parent.Next("bound")
		// The parent's revoke follows the hold on the same link, so it reaches the worker after it.
		parent.Send(&link.RevokeMessage{Revocation: jsonObject("session", "S1")})
		gone := goneOf(parent)
		if text := nexttest.JSONText(t, gone.Error); !strings.Contains(text, `"code":"forbidden"`) {
			t.Fatalf("gone error = %s, want forbidden", text)
		}
	})
}

// Mirrors "links and revoke messages › closes the child links whose principal it revokes, never the parent link".
func TestHostRevokeClosesTheChildLinksWhosePrincipalItRevokesNeverTheParentLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		address := serverAddress()
		host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), RevokedBy: revokedByApp, OnError: func(HostError) {}})
		defer closeHosts(t, host)
		must(t, host.Load(nil, background))
		var peers []*RawPeer
		var ends []*link.MemoryLink
		for _, principal := range []data.Principal{revocationS1, guestPrincipal("S2"), revocationS1} {
			a, b := link.MemoryLinkPair()
			must(t, host.Accept(a, LinkOptions{Principal: principal, Codec: testCodec()}, background))
			peers = append(peers, newRawPeer(t, b, rawPeerOptions{}))
			ends = append(ends, a, b)
		}
		parent, parentEnds := rawParentLinks(t, host)
		must(t, host.Revoke(jsonObject("session", "S1"), background))
		transported(append(ends, parentEnds[0], parentEnds[1])...)
		// Two tabs of one grant: both links close (race 4).
		if got := []bool{peers[0].Closed(), peers[1].Closed(), peers[2].Closed()}; !reflect.DeepEqual(got, []bool{true, false, true}) {
			t.Fatalf("closed = %v, want [true false true]", got)
		}
		if parent.Closed() {
			t.Fatal("the parent link closed")
		}
		// A revocation is no message for a child, nor for the parent.
		if kinds := peers[1].Kinds(); !reflect.DeepEqual(kinds, []string{"hello"}) {
			t.Fatalf("child kinds = %v, want [hello]", kinds)
		}
		if kinds := parent.Kinds(); len(kinds) != 0 {
			t.Fatalf("parent kinds = %v, want none", kinds)
		}
	})
}

// Mirrors "links and revoke messages › ignores a revoke from a child link (race 6)".
func TestHostIgnoresARevokeFromAChildLink(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		address := serverAddress()
		host := newHost(t, HostOptions{Address: &address, Principal: serverPrincipal(), RevokedBy: revokedByApp, OnError: func(HostError) {}})
		defer closeHosts(t, host)
		must(t, host.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error { _, err := serveNotes(h, nil); return err })}}, background))
		a, b := link.MemoryLinkPair()
		must(t, host.Accept(a, LinkOptions{Principal: revocationS1, Codec: testCodec()}, background))
		child := newRawPeer(t, b, rawPeerOptions{})
		child.Next("hello")
		holdAs(t, child, 0, revocationS1, Notes)
		child.Next("bound")
		child.Send(&link.RevokeMessage{Revocation: jsonObject("session", "S1")})
		transported(a, b)
		if child.Closed() {
			t.Fatal("the child link closed")
		}
		if kinds := child.Kinds(); len(kinds) != 0 {
			t.Fatalf("child kinds = %v, want none", kinds)
		}
	})
}

// Mirrors "links and revoke messages › sends revoke on every route link, and on none made later (race 5: nothing is replayed)".
func TestHostRevokeSendsRevokeOnEveryRouteLinkAndOnNoneMadeLater(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		workers := newRawWorkers(t, rawWorkerOptions{})
		r := relay(t, relayedOptions{Resolve: workers.Resolve})
		defer closeHosts(t, r.Tab, r.Server)
		_, err := r.Tab.Hold(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		scopeEventually(t, "the first worker was resolved", func() bool { return len(workers.Peers()) >= 1 })
		r.Transported()
		must(t, r.Server.Revoke(jsonObject("session", "S7"), background))
		revokesOf := func(peer *RawPeer) []link.Message {
			var revokes []link.Message
			for _, message := range peer.Received() {
				if message.Kind() == "revoke" {
					revokes = append(revokes, message)
				}
			}
			return revokes
		}
		scopeEventually(t, "the revoke reached the worker", func() bool { return len(revokesOf(workers.Peers()[0])) >= 1 })
		r.Transported()
		if revokes := revokesOf(workers.Peers()[0]); len(revokes) != 1 || !reflect.DeepEqual(revokes[0], &link.RevokeMessage{Revocation: jsonObject("session", "S7")}) {
			t.Fatalf("revokes = %v, want one revoke of S7", revokes)
		}
		_, err = r.Tab.Hold(PlacedNotes, jsonObject("session", "S2"), background, GetOptions{})
		must(t, err)
		scopeEventually(t, "the second worker was resolved", func() bool { return len(workers.Peers()) >= 2 })
		r.Transported()
		if slices.Contains(workers.Peers()[1].Kinds(), "revoke") {
			t.Fatal("a link made later got a revoke")
		}
	})
}

// Mirrors "GC of revoked objects › keeps no revoked object, however many references its holders still have".
func TestHostRevokeKeepsNoRevokedObject(t *testing.T) {
	host, parent, _, _ := revocationWorker(t, nil)
	defer closeHosts(t, host)
	chord := host.(*chordHost)
	refs := func() []weak.Pointer[chordObject] {
		holdAs(t, parent, 0, revocationS1, Notes)
		parent.Next("bound")
		var refs []weak.Pointer[chordObject]
		chord.enter(func() {
			for _, object := range chord.objectList() {
				if object.served.contract.Kind == contracts.KindRemote {
					refs = append(refs, weak.Make(object))
				}
			}
		})
		if len(refs) == 0 {
			t.Fatal("no remote object was attempted")
		}
		must(t, host.Revoke(jsonObject("session", "S1"), background))
		parent.Next("gone")
		return refs
	}()
	nexttest.ExpectCollected(t, refs...)
	if parent.Closed() {
		t.Fatal("the parent link closed")
	}
}

// Mirrors "revocation across a relay › closes a revoked tab's link at the server and ends its objects on the worker".
func TestHostRevocationAcrossARelayClosesARevokedTabsLinkAndEndsItsObjectsOnTheWorker(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		r := relay(t, relayedOptions{Tab: revocationS1, RevokedBy: revokedByApp})
		defer closeHosts(t, r.Tab, r.Server)
		notes, err := r.Tab.Hold(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		r.Transported()
		awaitRefState(t, "live", notes, func(s *RefState) bool { return s.Kind == RefStateLive })
		worker := r.Worker("session|S1")
		present := worker.References(PlacedNotes)
		if text := nexttest.JSONText(t, sourceValue(t, worker, present)); !strings.HasPrefix(text, "[{") || strings.Contains(text, "},{") {
			t.Fatalf("presence = %s, want one entry", text)
		}
		must(t, r.Server.Revoke(jsonObject("session", "S1"), background))
		r.Transported()
		r.Transported()
		// The tab's link closed: its reference waits for a new link, its holds on the worker are released.
		if kind := refStateOf(notes).Kind; kind != RefStateLoading {
			t.Fatalf("state = %s, want loading", kind)
		}
		if text := nexttest.JSONText(t, sourceValue(t, worker, present)); text != "[]" {
			t.Fatalf("presence = %s, want []", text)
		}
	})
}

// Mirrors "revocation across a relay › ends the worker's objects of the principal through the revoke message alone; the relay forwards their gone".
func TestHostRevocationAcrossARelayEndsTheWorkersObjectsThroughTheRevokeMessageAlone(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// The server's own revokedBy names no guest: its tab link stays, only the worker applies the revocation.
		r := relay(t, relayedOptions{Tab: revocationS1, RevokedBy: revokedByApp, ServerRevokedBy: func(data.Principal) ([]delta.JsonValue, error) { return nil, nil }})
		defer closeHosts(t, r.Tab, r.Server)
		notes, err := r.Tab.Hold(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		r.Transported()
		r.Transported()
		awaitRefState(t, "live", notes, func(s *RefState) bool { return s.Kind == RefStateLive })
		must(t, r.Server.Revoke(jsonObject("session", "S1"), background))
		r.Transported()
		r.Transported()
		if kind := refStateOf(notes).Kind; kind != RefStateEnded {
			t.Fatalf("state = %s, want ended", kind)
		}
		worker := r.Worker("session|S1")
		if text := nexttest.JSONText(t, sourceValue(t, worker, worker.References(PlacedNotes))); text != "[]" {
			t.Fatalf("presence = %s, want []", text)
		}
		if got := r.Errors.Tab.Len(); got != 0 {
			t.Fatalf("tab errors = %d, want 0", got)
		}
		// The tab's link stayed open: a new hold binds.
		again, err := r.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
		must(t, err)
		if kind := refStateOf(again).Kind; kind != RefStateLive {
			t.Fatalf("state = %s, want live", kind)
		}
	})
}

// guest is the principal of "revocation of this host's own requests over its paths": an owner, or a guest of a grant.
func grantGuest(grant string) data.Principal { return jsonObject("kind", "guest", "grant", grant) }

func grantRevokedBy(principal data.Principal) ([]delta.JsonValue, error) {
	if principal.Value("kind") == "guest" {
		return []delta.JsonValue{principal.Value("grant")}, nil
	}
	return nil, nil
}

var thereKind = mustKind(contracts.NewHostKind("app-there"))

// remoteThere is the file's `Remote`: placed on `There`, an empty view.
var remoteThere = mustContract(declareRemote("app.remote", contracts.RemoteDeclaration{
	Version:     1,
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return thereKind.Address(nil), nil },
	Variants:    []contracts.VariantEntry{contracts.Var("view", contracts.VariantDeclaration{})},
}))

// Mirrors "revocation of this host's own requests over its paths (fix round 1) › ends a guest's ctx.get waiting for its route before any link exists, at once (Astra 1, Sol 1)".
func TestHostRevokeEndsAGuestsCtxGetWaitingForItsRouteBeforeAnyLinkExists(t *testing.T) {
	host := newHost(t, HostOptions{Principal: jsonObject("kind", "owner"), RevokedBy: grantRevokedBy, OnError: func(HostError) {}})
	stuck := make(chan struct{})
	defer close(stuck)
	mustRoute(t)(host.Route(thereKind, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(_ contracts.Address, ctx context.Context) (ResolvedLink, error) {
			<-stuck
			return ResolvedLink{}, context.Cause(ctx)
		},
	}, background))
	must(t, host.Load(nil, background))
	outcome := make(chan error, 1)
	ready := time.Minute
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			_, err := ctx.Get(remoteThere, jsonObject(), GetOptions{ReadyTimeout: &ready})
			return nil, err
		}, background, RootOptions{Principal: grantGuest("g")})
		outcome <- err
	}()
	awaitUnbound(t, host)
	must(t, host.Revoke("g", background))
	err := <-outcome
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeForbidden || chordErr.Reason != data.ReasonRevoked {
		t.Fatalf("outcome = %v, want forbidden/revoked", err)
	}
	closeHosts(t, host)
}

// awaitUnbound waits until the host has a request waiting for its route (a binding in a path's holders or the index).
func awaitUnbound(t testing.TB, host Host) {
	t.Helper()
	h := host.(*chordHost)
	for {
		var waiting bool
		h.enter(func() { waiting = len(h.pathHolders()) > 0 || h.unbound.names.size() > 0 })
		if waiting {
			return
		}
		select {
		case <-t.Context().Done():
			t.Fatal("no request ever waited for its route")
		default:
		}
		turn(host)
	}
}

// Mirrors "› never lets the revoked request bind on the route's later link (Astra 1)".
func TestHostRevokeNeverLetsTheRevokedRequestBindOnTheRoutesLaterLink(t *testing.T) {
	host := newHost(t, HostOptions{Principal: jsonObject("kind", "owner"), RevokedBy: grantRevokedBy, OnError: func(HostError) {}})
	gate := make(chan ResolvedLink)
	mustRoute(t)(host.Route(thereKind, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve:   func(contracts.Address, context.Context) (ResolvedLink, error) { return <-gate, nil },
	}, background))
	must(t, host.Load(nil, background))
	pending := make(chan error, 1)
	ready := 500 * time.Millisecond
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			if _, err := ctx.Get(remoteThere, jsonObject(), GetOptions{ReadyTimeout: &ready}); err != nil {
				return nil, err
			}
			return nil, errors.New("revoked request became live")
		}, background, RootOptions{Principal: grantGuest("g")})
		pending <- err
	}()
	awaitUnbound(t, host)
	must(t, host.Revoke("g", background))
	address := thereKind.Address(nil)
	worker := newHost(t, HostOptions{Address: &address, Principal: jsonObject("kind", "owner"), RevokedBy: grantRevokedBy, OnError: func(HostError) {}})
	up, down := link.MemoryLinkPair()
	must(t, worker.Connect(up, LinkOptions{Principal: jsonObject("kind", "owner"), Trusted: true}, background))
	facet := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(remoteThere, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "view"}, nil
		}, ServeOptions{})
	})
	must(t, worker.Load([]HostFacet{{Name: "@test/late", Facet: facet}}, background))
	gate <- ResolvedLink{Link: down, Principal: jsonObject("kind", "owner"), Trusted: true}
	err := <-pending
	if chordErr, ok := data.IsChordError(err); !ok || chordErr.Code != data.CodeForbidden || chordErr.Reason != data.ReasonRevoked {
		t.Fatalf("outcome = %v, want forbidden/revoked", err)
	}
	transported(up, down)
	if got := activityOf(worker).References; got != 0 {
		t.Fatalf("worker references = %d, want 0", got)
	}
	closeHosts(t, host, worker)
}

// Mirrors "› ends a guest's reference over a route link that dropped, and nothing holds it on the next one".
func TestHostRevokeEndsAGuestsReferenceOverARouteLinkThatDroppedAndNothingHoldsItOnTheNextOne(t *testing.T) {
	var mu sync.Mutex
	var routed []*RawPeer
	var ends []*link.MemoryLink
	host := newHost(t, HostOptions{Principal: jsonObject("kind", "owner"), RevokedBy: grantRevokedBy, OnError: func(HostError) {}})
	mustRoute(t)(host.Route(thereKind, RouteOptions{
		Authorize: func(data.Principal, contracts.Address) (bool, error) { return true, nil },
		Resolve: func(contracts.Address, context.Context) (ResolvedLink, error) {
			a, b := link.MemoryLinkPair()
			mu.Lock()
			routed = append(routed, newRawPeer(t, b, rawPeerOptions{}))
			ends = append(ends, a, b)
			mu.Unlock()
			return ResolvedLink{Link: a, Principal: jsonObject("kind", "owner"), Codec: testCodec()}, nil
		},
	}, background))
	must(t, host.Load(nil, background))
	states := make(chan string, 1)
	ready := time.Minute
	go func() {
		_, err := host.Root(func(ctx Ctx) (any, error) {
			if _, err := ctx.Get(remoteThere, jsonObject(), GetOptions{ReadyTimeout: &ready}); err != nil {
				states <- err.Error()
			} else {
				states <- "live"
			}
			return nil, nil
		}, background, RootOptions{Principal: grantGuest("g")})
		if err != nil {
			t.Errorf("root: %v", err)
		}
	}()
	for {
		mu.Lock()
		have := len(routed)
		mu.Unlock()
		if have > 0 {
			break
		}
		turn(host)
	}
	mu.Lock()
	first := routed[0]
	mu.Unlock()
	first.Next("hold")
	first.Close()
	mu.Lock()
	all := append([]*link.MemoryLink(nil), ends...)
	mu.Unlock()
	transported(all...)
	must(t, host.Revoke("g", background))
	if got := <-states; got != "app.remote: revoked" {
		t.Fatalf("outcome = %q, want %q", got, "app.remote: revoked")
	}
	mu.Lock()
	defer mu.Unlock()
	for i, peer := range routed {
		if i == 0 {
			continue
		}
		for _, message := range peer.Received() {
			if message.Kind() == "hold" {
				t.Fatalf("link %d got a hold", i)
			}
		}
	}
	closeHosts(t, host)
}

// rawParentOnLoop is rawParentLinks over a memory link on the host's own microtask queue (link.MemoryLinkPairOn), as Pi's
// memory link is on the one global queue the host queues its microtasks on; the returned function runs a section of the
// host's loop, where a peer sends several messages in one turn.
func rawParentOnLoop(t testing.TB, host Host) (*RawPeer, [2]*link.MemoryLink, sectionLoop) {
	t.Helper()
	loop := host.(*chordHost).loop()
	a, b := link.MemoryLinkPairOn(loop.Queue, func() { loop.Run(func() {}); loop.Run(func() {}) })
	must(t, host.Connect(a, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
	peer := newRawPeer(t, b, rawPeerOptions{})
	peer.Next("hello")
	return peer, [2]*link.MemoryLink{a, b}, loop
}
