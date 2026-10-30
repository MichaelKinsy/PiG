package host

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports packages/chord/test/next/remote/host-link.test.ts.
//
// Go forms: a Promise a test starts and awaits later (`const got = tab.get(...)`) is a goroutine delivering on a
// channel (rhAsync); `await transported()` is rhSync / rhSettleLinks.

// rhAsync runs work on a goroutine of its own, as `const p = work()` starts a Promise; wait returns its outcome.
type rhAsync[T any] struct {
	done  chan struct{}
	value T
	err   error
}

func rhGo[T any](work func() (T, error)) *rhAsync[T] {
	a := &rhAsync[T]{done: make(chan struct{})}
	go func() {
		defer close(a.done)
		a.value, a.err = work()
	}()
	return a
}

func (a *rhAsync[T]) wait() (T, error) {
	<-a.done
	return a.value, a.err
}

func (a *rhAsync[T]) must(t testing.TB) T {
	t.Helper()
	value, err := a.wait()
	must(t, err)
	return value
}

var (
	rhServerWho = rhRemote("app.server-who", []contracts.Property{rhProp("who", contracts.Str())},
		contracts.Meth("echo", contracts.NewMethod(contracts.Str(), contracts.Str())))
	rhAsker = mustContract(contracts.Remote("app.asker", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str())}, HostAddress: exSessionAddress,
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
			State:   []contracts.Property{rhProp("who", contracts.Str())},
			Methods: []contracts.MethodEntry{contracts.Meth("ask", contracts.NewMethod(contracts.Str(), contracts.Str()))},
		})},
	}))
	rhNeighbour = mustContract(contracts.Remote("app.neighbour", contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("other", contracts.Str())},
		HostAddress: exSessionAddress,
		Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
			State: []contracts.Property{rhProp("count", contracts.Int())},
		})},
	}))
)

func rhServeWho(host FacetHost) error {
	return host.Serve(rhServerWho, func(_ *delta.JsonObject, principal data.Principal, _ Self) (contracts.Implementation, error) {
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"who": rhConst(principalKind(principal))},
			Methods: map[string]contracts.MethodFunction{
				"echo": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					who := ""
					if principal := ctx.(Ctx).Principal(); principal != nil {
						who = principalKind(principal)
					}
					return who + ":" + args.(string), nil
				},
			},
		}}, nil
	}, ServeOptions{})
}

func rhServeAsker(host FacetHost) error {
	if err := host.Serve(rhAsker, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		server := self.Get(rhServerWho, jsonObject())
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"who": Expose(server.Member("who"))},
			Methods: map[string]contracts.MethodFunction{
				"ask": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					got, err := ctx.(Ctx).Get(rhServerWho, jsonObject(), GetOptions{})
					if err != nil {
						return nil, err
					}
					return got.Call("echo", args, ctx)
				},
			},
		}}, nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return host.Serve(rhNeighbour, func(params *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
		notes := self.Get(PlacedNotes, jsonObject("session", params.Value("other")))
		return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"count": Expose(notes.Member("count"))},
		}}, nil
	}, ServeOptions{})
}

func rhRefValue(t testing.TB, ref *Reference, member string) delta.JsonValue {
	t.Helper()
	value, err := ref.Member(member).Value()
	must(t, err)
	return value
}

// rhAwaitCount waits until member of ref has value want, through a watch (the frames carry it).
func rhAwaitValue(t testing.TB, ref *Reference, member string, want delta.JsonValue) {
	t.Helper()
	watch, err := ref.Member(member).Watch(background)
	must(t, err)
	frames := nexttest.FramesOf(watch)
	rhWaitFor(t, func() bool {
		items := frames.Items()
		if len(items) > 0 {
			return rhJSON(t, items[len(items)-1].Value) == rhJSON(t, want)
		}
		return rhJSON(t, watch.Value()) == rhJSON(t, want)
	})
}

// rhPlacedNotes is a worker's address of a session.
func rhPlacedAddress(session string) link.WireAddress {
	return link.WireAddress{Kind: "session", Params: jsonObject("id", session)}
}

// rhBindPlaced binds a hold a raw worker received (its id and origin): PlacedNotes's editor variant, no notes.
func rhBindPlaced(t testing.TB, peer *RawPeer, hold *link.HoldMessage) {
	t.Helper()
	editor := editorWire(t)
	notes, err := link.JsonPart([]any{})
	must(t, err)
	count, err := link.JsonPart(float64(0))
	must(t, err)
	peer.Send(&link.BoundMessage{Origin: hold.Origin, Holds: []link.BoundEntry{{
		Hold: hold.Hold, Variant: editor.Variant, Failed: []link.FailedMember{},
		Members: []link.BoundMember{{Member: editor.State["notes"], Value: notes}, {Member: editor.State["count"], Value: count}},
	}}})
}

func TestHostLinkTwoSidedLinks(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// host-link.test.ts: two-sided links › carry the server's holds of a worker type and the worker's holds of a server type over one route link
		t.Run("carry the server's holds of a worker type and the worker's holds of a server type over one route link", func(t *testing.T) {
			rig := relay(t, relayedOptions{Server: rhServeWho, Worker: rhServeAsker})
			asker, err := rig.Tab.Get(rhAsker, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			// The worker's factory held `ServerWho` over its parent link, the route link the server made.
			if who := rhRefValue(t, asker, "who"); who != "worker" {
				t.Fatalf("who = %v, want worker", who)
			}
			if links := rig.RouteLinks(); len(links) != 1 {
				t.Fatalf("route links = %d, want 1", len(links))
			}
		})

		// host-link.test.ts: two-sided links › carry calls both ways with equal ids: each side's ids are its own
		t.Run("carry calls both ways with equal ids: each side's ids are its own", func(t *testing.T) {
			rig := relay(t, relayedOptions{Server: rhServeWho, Worker: rhServeAsker})
			asker, err := rig.Tab.Get(rhAsker, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			// Server to worker: call 0 on the route link; inside it, worker to server: call 0 the other way.
			got, err := asker.Call("ask", "x", background)
			must(t, err)
			if got != "worker:x" {
				t.Fatalf("ask = %v, want worker:x", got)
			}
			a := rhGo(func() (delta.JsonValue, error) { return asker.Call("ask", "a", background) })
			b := rhGo(func() (delta.JsonValue, error) { return asker.Call("ask", "b", background) })
			if va, vb := a.must(t), b.must(t); va != "worker:a" || vb != "worker:b" {
				t.Fatalf("asks = %v, %v", va, vb)
			}
		})

		// host-link.test.ts: two-sided links › let a worker reach another worker through the server
		t.Run("let a worker reach another worker through the server", func(t *testing.T) {
			rig := relay(t, relayedOptions{Worker: rhServeAsker})
			neighbour, err := rig.Tab.Get(rhNeighbour, jsonObject("session", "S1", "other", "S2"), background, GetOptions{})
			must(t, err)
			if got := rig.Resolves(); len(got) != 2 || got[0] != "session|S1" || got[1] != "session|S2" {
				t.Fatalf("resolves = %v, want [session|S1 session|S2]", got)
			}
			addNote(t, rig.Worker("session|S2"), "S2", "a")
			rhAwaitValue(t, neighbour, "count", float64(1))
		})

		// host-link.test.ts: two-sided links › apply this host's own messages and forward the tab origin's, each acknowledged by its consumer
		t.Run("apply this host's own messages and forward the tab origin's, each acknowledged by its consumer", func(t *testing.T) {
			workers := newRawWorkers(t, rawWorkerOptions{})
			rig := relay(t, relayedOptions{Resolve: workers.Resolve})
			own := rhGo(func() (*Reference, error) {
				return rig.Server.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			})
			worker := rhFirstPeer(t, workers)
			first := worker.Next("hold").(*link.HoldMessage)
			rhBindPlaced(t, worker, first)
			relayedHold := rhGo(func() (*Reference, error) {
				return rig.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			})
			second := worker.Next("hold").(*link.HoldMessage)
			if first.Origin.Ok || !second.Origin.Ok || second.Origin.Value != 1 {
				t.Fatalf("origins = %+v, %+v; want none, 1", first.Origin, second.Origin)
			}
			rhBindPlaced(t, worker, second)
			serverNotes, tabs := own.must(t), relayedHold.must(t)
			// The acks of the two `bound` messages.
			for _, origin := range []link.Optional[int]{{}, link.Some(1)} {
				if ack := worker.Next("ack").(*link.AckMessage); ack.Origin != origin {
					t.Fatalf("ack = %+v, want %+v", ack.Origin, origin)
				}
			}
			editor := editorWire(t)
			ops, err := link.JsonPart([]any{[]any{"r", float64(5)}})
			must(t, err)
			for _, hold := range []*link.HoldMessage{first, second} {
				worker.Send(&link.StateMessage{Origin: hold.Origin, Holds: []link.StateEntry{{
					Hold: hold.Hold, Members: []link.StateMember{{Member: editor.State["count"], Ops: ops}},
					Failed: []link.FailedMember{}, Retired: link.NoRetired,
				}}})
			}
			// Each state is acknowledged by its consumer once applied, the tab origin's with its origin.
			for _, origin := range []link.Optional[int]{{}, link.Some(1)} {
				if ack := worker.Next("ack").(*link.AckMessage); ack.Origin != origin {
					t.Fatalf("ack = %+v, want %+v", ack.Origin, origin)
				}
			}
			for _, ref := range []*Reference{serverNotes, tabs} {
				rhAwaitValue(t, ref, "count", float64(5))
			}
			rhSync(t, worker)
			rhKindsAre(t, worker, "hello")
		})
	})
}

// rhFirstPeer waits for the first raw worker a route resolved (it is made by the route's off-loop resolve).
func rhFirstPeer(t testing.TB, workers *rawWorkers) *RawPeer { return rhPeerAt(t, workers, 0) }

func rhPeerAt(t testing.TB, workers *rawWorkers, index int) *RawPeer {
	t.Helper()
	var peer *RawPeer
	rhWaitFor(t, func() bool {
		peers := workers.Peers()
		if index < len(peers) {
			peer = peers[index]
		}
		return peer != nil
	})
	return peer
}

// --- principals on holds ---------------------------------------------------------------------------------------------

var rhGateway = rhRemote("app.gateway", []contracts.Property{rhProp("n", contracts.Int())},
	contracts.Meth("open", contracts.NewMethod(contracts.Str(), contracts.Int())))

// With params: not activated by `load`, which would wait for the raw worker.
var rhView = mustContract(contracts.Local("app.view", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

type rhPrincipals struct {
	*relayed
	workers *rawWorkers
}

// rhPrincipalsRig is a server serving a guest-facing gateway and a local view, both reaching PlacedNotes on a raw worker.
func rhPrincipalsRig(t testing.TB, tab data.Principal) *rhPrincipals {
	t.Helper()
	workers := newRawWorkers(t, rawWorkerOptions{})
	if tab == nil {
		tab = guestPrincipal("S1")
	}
	rig := relay(t, relayedOptions{Tab: tab, Resolve: workers.Resolve, Server: func(host FacetHost) error {
		if err := host.Serve(rhGateway, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			// `self.get` in a remote object: the object's principal, this host's incarnation.
			own := self.Get(PlacedNotes, jsonObject("session", "own"))
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(own.Member("count"))},
				Methods: map[string]contracts.MethodFunction{
					// `ctx.get` in a handler: the caller's principal and incarnation.
					"open": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						got, err := ctx.(Ctx).Get(PlacedNotes, jsonObject("session", args), GetOptions{})
						if err != nil {
							return nil, err
						}
						return got.Member("count").Value()
					},
				},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(rhView, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": Expose(self.Get(PlacedNotes, jsonObject("session", "view")).Member("count"))},
			}}, nil
		}, ServeOptions{})
	}})
	return &rhPrincipals{relayed: rig, workers: workers}
}

// rhHoldInfo is what a raw worker's next hold says: its session and whom it acts for.
type rhHoldInfo struct {
	session     string
	principal   string
	incarnation string
}

// nextHold is the next hold the raw worker of peers[index] receives, bound at once, with whom it acts for.
func (p *rhPrincipals) nextHold(t testing.TB, index int) rhHoldInfo {
	t.Helper()
	peer := rhPeerAt(t, p.workers, index)
	hold := peer.Next("hold").(*link.HoldMessage)
	rhBindPlaced(t, peer, hold)
	params := partValue(t, hold.Params).(*delta.JsonObject)
	info := rhHoldInfo{session: params.Value("session").(string), incarnation: hold.Incarnation}
	if hold.Principal != nil {
		info.principal = rhJSON(t, hold.Principal)
	}
	return info
}

func rhPrincipalJSON(t testing.TB, principal data.Principal) string { return rhJSON(t, principal) }

func TestHostLinkPrincipalsOnHolds(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		check := func(t *testing.T, got rhHoldInfo, session string, principal data.Principal, incarnation string) {
			t.Helper()
			want := rhHoldInfo{session: session, principal: rhPrincipalJSON(t, principal), incarnation: incarnation}
			if got != want {
				t.Fatalf("hold = %+v, want %+v", got, want)
			}
		}

		// host-link.test.ts: principals on holds (section 4's table) › a relayed hold acts as the link's principal: a forged principal from a tab is ignored (race 9)
		t.Run("a relayed hold acts as the link's principal: a forged principal from a tab is ignored (race 9)", func(t *testing.T) {
			rig := rhPrincipalsRig(t, nil)
			a, b := link.MemoryLinkPair()
			must(t, rig.Server.Accept(a, LinkOptions{Principal: guestPrincipal("S1"), Codec: testCodec()}, background))
			tab := newRawPeer(t, b, rawPeerOptions{})
			params, err := link.JsonPart(jsonObject("session", "S1"))
			must(t, err)
			tab.Send(&link.HoldMessage{
				Hold: 0, Type: PlacedNotes.Name, Version: PlacedNotes.Version, Fingerprint: PlacedNotes.Fingerprint, Params: params,
				Address: rhPlacedAddress("S1"), Principal: ownerPrincipal(),
			})
			check(t, rig.nextHold(t, 0), "S1", guestPrincipal("S1"), rawPeerIncarnation)
		})

		// host-link.test.ts: principals on holds (section 4's table) › a hold relayed over a parent path names nobody: a child's principal and incarnation stay behind
		t.Run("a hold relayed over a parent path names nobody: a child's principal and incarnation stay behind", func(t *testing.T) {
			worker := rhCreate(t, HostOptions{Principal: workerPrincipal("S1"), OnError: func(HostError) {}})
			must(t, worker.Load(nil, background))
			up, parentEnd := link.MemoryLinkPair()
			must(t, worker.Connect(up, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			parent := newRawPeer(t, parentEnd, rawPeerOptions{})
			a, b := link.MemoryLinkPair()
			must(t, worker.Accept(a, LinkOptions{Principal: guestPrincipal("S1"), Codec: testCodec()}, background))
			child := newRawPeer(t, b, rawPeerOptions{})
			params, err := link.JsonPart(jsonObject("session", "S1"))
			must(t, err)
			child.Send(&link.HoldMessage{
				Hold: 0, Type: PlacedNotes.Name, Version: PlacedNotes.Version, Fingerprint: PlacedNotes.Fingerprint, Params: params,
				Address: rhPlacedAddress("S1"), Principal: ownerPrincipal(), Incarnation: rawPeerIncarnation,
			})
			relayedHold := parent.Next("hold").(*link.HoldMessage)
			if relayedHold.Hold != 0 || !relayedHold.Origin.Ok || relayedHold.Origin.Value != 1 || relayedHold.Type != PlacedNotes.Name {
				t.Fatalf("relayed hold = %+v", relayedHold)
			}
			if relayedHold.Principal != nil || relayedHold.Incarnation != "" {
				t.Fatalf("relayed hold names %v %q", relayedHold.Principal, relayedHold.Incarnation)
			}
			must(t, worker.Close(background))
		})

		// host-link.test.ts: principals on holds (section 4's table) › host.get through a route acts as the host: its principal and incarnation
		t.Run("host.get through a route acts as the host: its principal and incarnation", func(t *testing.T) {
			rig := rhPrincipalsRig(t, nil)
			rhGo(func() (*Reference, error) {
				return rig.Server.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			})
			check(t, rig.nextHold(t, 0), "S1", serverPrincipal(), incarnationOf(rig.Server))
		})

		// host-link.test.ts: principals on holds (section 4's table) › ctx.get in a guest's call acts as the guest, with the tab's incarnation (race 10)
		t.Run("ctx.get in a guest's call acts as the guest, with the tab's incarnation (race 10)", func(t *testing.T) {
			rig := rhPrincipalsRig(t, nil)
			got := rhGo(func() (*Reference, error) { return rig.Tab.Get(rhGateway, jsonObject(), background, GetOptions{}) })
			if info := rig.nextHold(t, 0); info.session != "own" {
				t.Fatalf("session = %q, want own", info.session)
			}
			gateway := got.must(t)
			opened := rhGo(func() (delta.JsonValue, error) { return gateway.Call("open", "S1", background) })
			// Another address: another worker.
			check(t, rig.nextHold(t, 1), "S1", guestPrincipal("S1"), incarnationOf(rig.Tab))
			if value := opened.must(t); value != float64(0) {
				t.Fatalf("open = %v, want 0", value)
			}
		})

		// host-link.test.ts: principals on holds (section 4's table) › self.get in a remote object acts as that object's principal, with this host's incarnation
		t.Run("self.get in a remote object acts as that object's principal, with this host's incarnation", func(t *testing.T) {
			rig := rhPrincipalsRig(t, nil)
			_, err := rig.Tab.Hold(rhGateway, jsonObject(), background, GetOptions{})
			must(t, err)
			check(t, rig.nextHold(t, 0), "own", guestPrincipal("S1"), incarnationOf(rig.Server))
		})

		// host-link.test.ts: principals on holds (section 4's table) › self.get in a local object acts as the host
		t.Run("self.get in a local object acts as the host", func(t *testing.T) {
			rig := rhPrincipalsRig(t, nil)
			rhGo(func() (*Reference, error) {
				return rig.Server.Get(rhView, jsonObject("id", float64(1)), background, GetOptions{})
			})
			check(t, rig.nextHold(t, 0), "view", serverPrincipal(), incarnationOf(rig.Server))
		})

		// host-link.test.ts: principals on holds (section 4's table) › two origins with equal principal keys but other incarnations are two upstream holds
		t.Run("two origins with equal principal keys but other incarnations are two upstream holds", func(t *testing.T) {
			rig := rhPrincipalsRig(t, ownerPrincipal())
			other := rhCreate(t, HostOptions{OnError: func(HostError) {}})
			must(t, other.Load(nil, background))
			a, b := link.MemoryLinkPair()
			must(t, rig.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
			must(t, other.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec()}, background))
			rhGo(func() (*Reference, error) {
				return rig.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			})
			first := rig.nextHold(t, 0)
			rhGo(func() (*Reference, error) {
				return other.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			})
			second := rig.nextHold(t, 0)
			check(t, first, "S1", ownerPrincipal(), incarnationOf(rig.Tab))
			check(t, second, "S1", ownerPrincipal(), incarnationOf(other))
		})

		// host-link.test.ts: principals on holds (section 4's table) › upward is a boundary: a worker's hold of a server type acts as the worker, whoever it serves (race 11)
		t.Run("upward is a boundary: a worker's hold of a server type acts as the worker, whoever it serves (race 11)", func(t *testing.T) {
			rig := relay(t, relayedOptions{Tab: guestPrincipal("S1"), Server: rhServeWho, Worker: rhServeAsker})
			asker, err := rig.Tab.Get(rhAsker, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			got, err := asker.Call("ask", "x", background)
			must(t, err)
			if got != "worker:x" {
				t.Fatalf("ask = %v, want worker:x", got)
			}
		})
	})
}

func TestHostLinkTrustedLinks(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// host-link.test.ts: trusted links › work as untrusted ones do
		t.Run("work as untrusted ones do", func(t *testing.T) {
			rig := relay(t, relayedOptions{Trusted: true})
			notes, err := rig.Tab.Get(PlacedNotes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			variant, bound, err := notes.Variant()
			must(t, err)
			if !bound || variant != "editor" {
				t.Fatalf("variant = %q bound %v, want editor", variant, bound)
			}
		})
	})
}

// --- parts: compression at the origin and limits --------------------------------------------------------------------

// rhCounting is a compressor that counts what it does (nodeCompression underneath).
type rhCounting struct {
	mu                 sync.Mutex
	deflates, inflates int
}

func (c *rhCounting) Deflate(bytes []byte) []byte {
	c.mu.Lock()
	c.deflates++
	c.mu.Unlock()
	return nextnode.NodeCompression.Deflate(bytes)
}

func (c *rhCounting) Inflate(bytes []byte, limit float64) nextnode.Inflated {
	c.mu.Lock()
	c.inflates++
	c.mu.Unlock()
	return nextnode.NodeCompression.Inflate(bytes, limit)
}

func (c *rhCounting) counts() (deflates, inflates int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.deflates, c.inflates
}

// rhLarge is a note text large enough that a notes list with it is deflated at its origin.
var rhLarge = strings.Repeat("lorem ipsum ", 2000)

type rhCompressing struct {
	Server, Client Host
	Errors         *hostErrors
	Metrics        *nexttest.Meter
}

// rhCompress is a server serving Notes with compression, and a client with its own compression (none: it inflates
// asynchronously), its parent link's limits limits.
func rhCompress(t testing.TB, server, client link.Compression, limits link.PartLimits) *rhCompressing {
	t.Helper()
	errs := &hostErrors{}
	meter := nexttest.Metered(t)
	result := &rhCompressing{Errors: errs, Metrics: meter}
	result.Server = rhCreate(t, HostOptions{
		Address: new(Server.Address(nil)), Principal: serverPrincipal(), Compression: server, OnError: errs.push,
	})
	must(t, result.Server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		_, err := serveNotes(host, nil)
		return err
	})}}, background))
	result.Client = rhCreate(t, HostOptions{Compression: client, Telemetry: meter.Telemetry, OnError: errs.push})
	must(t, result.Client.Load(nil, background))
	a, b := link.MemoryLinkPair()
	must(t, result.Server.Accept(a, LinkOptions{Principal: ownerPrincipal(), Codec: testCodec()}, background))
	must(t, result.Client.Connect(b, LinkOptions{Principal: serverPrincipal(), Codec: testCodec(), Limits: limits}, background))
	return result
}

func rhNoteTexts(t testing.TB, notes *Reference) []string {
	t.Helper()
	list, ok := rhRefValue(t, notes, "notes").([]any)
	if !ok {
		t.Fatalf("notes = %v", rhRefValue(t, notes, "notes"))
	}
	texts := make([]string, len(list))
	for i, note := range list {
		texts[i] = note.(*delta.JsonObject).Value("text").(string)
	}
	return texts
}

func TestHostLinkPartsCompressionAndLimits(t *testing.T) {
	forEachCodec(t, func(t *testing.T) {
		// host-link.test.ts: parts: compression at the origin and limits › deflate JSON parts above 16 KiB at their origin and inflate them where owned, synchronously; never up a parent link's arguments
		t.Run("deflate JSON parts above 16 KiB at their origin and inflate them where owned, synchronously; never up a parent link's arguments", func(t *testing.T) {
			server, client := &rhCounting{}, &rhCounting{}
			hosts := rhCompress(t, server, client, link.PartLimits{})
			addNote(t, hosts.Server, "S1", rhLarge)
			notes, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			if variant, _, _ := notes.Variant(); variant != "editor" {
				t.Fatalf("an owner's notes are the editor's, got %q", variant)
			}
			if texts := rhNoteTexts(t, notes); len(texts) != 1 || texts[0] != rhLarge {
				t.Fatalf("notes = %d texts", len(texts))
			}
			if deflates, _ := server.counts(); deflates != 1 {
				t.Fatalf("server deflates = %d, want 1", deflates)
			}
			if _, inflates := client.counts(); inflates != 1 {
				t.Fatalf("client inflates = %d, want 1", inflates)
			}
			// A large argument goes up the client's parent link uncompressed: child links refuse compressed parts.
			_, err = notes.Call("add", jsonObject("text", rhLarge), background)
			must(t, err)
			if deflates, _ := client.counts(); deflates != 0 {
				t.Fatalf("client deflates = %d, want 0", deflates)
			}
			if _, inflates := server.counts(); inflates != 0 {
				t.Fatalf("server inflates = %d, want 0", inflates)
			}
			rhWaitFor(t, func() bool { return len(rhNoteTexts(t, notes)) == 2 })
		})

		// host-link.test.ts: parts: compression at the origin and limits › inflate asynchronously without a compressor: later messages of the link wait, in order
		t.Run("inflate asynchronously without a compressor: later messages of the link wait, in order", func(t *testing.T) {
			hosts := rhCompress(t, nextnode.NodeCompression, nil, link.PartLimits{})
			addNote(t, hosts.Server, "S1", rhLarge)
			notes, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			if texts := rhNoteTexts(t, notes); len(texts) != 1 || texts[0] != rhLarge {
				t.Fatalf("notes = %d texts", len(texts))
			}
			watch, err := notes.Member("count").Watch(background)
			must(t, err)
			counts := nexttest.FramesOf(watch)
			// Each commit sends the whole changed list (a derive's diff appends one note): large ones deflated.
			for i := range 5 {
				text := "small"
				if i%2 == 0 {
					text = rhLarge
				}
				addNote(t, hosts.Server, "S1", text)
			}
			rhWaitFor(t, func() bool { return rhRefValue(t, notes, "count") == float64(6) })
			texts := rhNoteTexts(t, notes)
			want := []string{rhLarge, rhLarge, "small", rhLarge, "small", rhLarge}
			if len(texts) != len(want) {
				t.Fatalf("notes = %d, want %d", len(texts), len(want))
			}
			for i := range want {
				if len(texts[i]) != len(want[i]) {
					t.Fatalf("note %d has %d bytes, want %d", i, len(texts[i]), len(want[i]))
				}
			}
			var seen []float64
			for _, frame := range counts.Items() {
				seen = append(seen, frame.Value.(float64))
			}
			for i := 1; i < len(seen); i++ {
				if seen[i] < seen[i-1] {
					t.Fatalf("counts out of order: %v", seen)
				}
			}
		})

		// host-link.test.ts: parts: compression at the origin and limits › fail only the hold of a compressed bomb, on either inflate path (race 11): the link stays
		t.Run("fail only the hold of a compressed bomb, on either inflate path (race 11): the link stays", func(t *testing.T) {
			for _, client := range []link.Compression{nextnode.NodeCompression, nil} {
				decoded := float64(100_000)
				hosts := rhCompress(t, nextnode.NodeCompression, client, link.PartLimits{Decoded: &decoded})
				addNote(t, hosts.Server, "S1", strings.Repeat("x", 200_000))
				addNote(t, hosts.Server, "S2", "small")
				_, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
				chordErr, ok := data.IsChordErrorCode(err, data.CodeTooLarge)
				if !ok || chordErr.Reason != data.ReasonPart {
					t.Fatalf("get = %v, want too_large/part", err)
				}
				other, err := hosts.Client.Get(Notes, jsonObject("session", "S2"), background, GetOptions{})
				must(t, err)
				if texts := rhNoteTexts(t, other); len(texts) != 1 || texts[0] != "small" {
					t.Fatalf("notes = %v", texts)
				}
			}
		})

		// host-link.test.ts: parts: compression at the origin and limits › cost nothing for uncompressed messages at a host without a compressor: no inflate, no stream
		t.Run("cost nothing for uncompressed messages at a host without a compressor: no inflate, no stream", func(t *testing.T) {
			hosts := rhCompress(t, nextnode.NodeCompression, nil, link.PartLimits{})
			notes, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			for range 5 {
				addNote(t, hosts.Server, "S1", "small")
			}
			rhWaitFor(t, func() bool { return rhRefValue(t, notes, "count") == float64(5) })
			if inflates := hosts.Metrics.Counts()["chord.link.inflates"]; inflates != 0 {
				t.Fatalf("inflates = %v, want 0", inflates)
			}
			if errs := hosts.Errors.All(); len(errs) != 0 {
				t.Fatalf("errors = %v", errs)
			}
		})

		// host-link.test.ts: parts: compression at the origin and limits › send every part uncompressed from a host without a compressor
		t.Run("send every part uncompressed from a host without a compressor", func(t *testing.T) {
			server := &rhCounting{}
			hosts := rhCompress(t, server, nil, link.PartLimits{})
			notes, err := hosts.Client.Get(Notes, jsonObject("session", "S1"), background, GetOptions{})
			must(t, err)
			if variant, _, _ := notes.Variant(); variant != "editor" {
				t.Fatalf("an owner's notes are the editor's, got %q", variant)
			}
			_, err = notes.Call("add", jsonObject("text", rhLarge), background)
			must(t, err)
			if _, inflates := server.counts(); inflates != 0 {
				t.Fatalf("server inflates = %d, want 0", inflates)
			}
		})
	})
}

// --- routines over a link --------------------------------------------------------------------------------------------

// routines.test.ts: on › on a remote member failed since its binding (no value yet), waits for its first value
func TestOnOnARemoteMemberFailedSinceItsBindingNoValueYetWaitsForItsFirstValue(t *testing.T) {
	halves := rhRemote("app.halves", []contracts.Property{rhProp("half", contracts.Int())},
		contracts.Meth("set", contracts.NewMethod(contracts.Int())))
	server := rhCreate(t, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	must(t, server.Load([]HostFacet{{Name: "@test/app", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(halves, func(_ *delta.JsonObject, _ data.Principal, self Self) (contracts.Implementation, error) {
			doc := stateOf(self, rhCounter)
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{
					"half": self.Derive([]any{doc.s}, func(values []any) (delta.JsonValue, error) {
						n := values[0].(*delta.JsonObject).Value("n").(float64)
						if n == 2 {
							return nil, errors.New("half threw")
						}
						return n / 2, nil
					}),
				},
				Methods: map[string]contracts.MethodFunction{
					"set": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return nil, doc.Change(func(draft *delta.Object) { _ = draft.Set("n", args) }, ctx)
					},
				},
			}}, nil
		}, ServeOptions{})
	})}}, background))
	ref, err := server.Get(halves, jsonObject(), background, GetOptions{})
	must(t, err)
	_, err = ref.Call("set", float64(2), background)
	must(t, err)
	viewer := mustContract(contracts.Local("app.viewer", contracts.LocalDeclaration{Members: []contracts.LocalEntry{},
		Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Int())},
	}))
	var mu sync.Mutex
	var calls []any
	errs := &hostErrors{}
	client := rhCreate(t, HostOptions{OnError: errs.push})
	must(t, client.Load([]HostFacet{{Name: "@test/client", Facet: DefineFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(viewer, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			self.On(self.Get(halves, jsonObject()).Member("half"), func(call chord.WatchCall[delta.JsonValue], _ Ctx) *Handled {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, []any{call.Value, call.Ops})
				return nil
			})
			return contracts.Implementation{}, nil
		}, ServeOptions{})
	})}}, background))
	a, b := link.MemoryLinkPair()
	// The server's own principal: the client holds the object `halves` refers to.
	must(t, server.Accept(a, LinkOptions{Principal: serverPrincipal()}, background))
	must(t, client.Connect(b, LinkOptions{Principal: serverPrincipal()}, background))
	_, err = client.Get(viewer, jsonObject("id", float64(1)), background, GetOptions{})
	must(t, err)
	rhSettleLinks(a, b)
	client.(*chordHost).loop().Run(func() {})
	mu.Lock()
	if len(calls) != 0 || errs.Len() != 0 {
		t.Fatalf("calls %v, errors %v; want none", calls, errs.All())
	}
	mu.Unlock()
	_, err = ref.Call("set", float64(4), background)
	must(t, err)
	rhWaitFor(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(calls) >= 1 })
	rhSettleLinks(a, b)
	mu.Lock()
	defer mu.Unlock()
	rhEqual(t, "calls", calls, []any{[]any{2, []any{[]any{"r", 2}}}})
	if errs.Len() != 0 {
		t.Fatalf("errors = %v", errs.All())
	}
}
