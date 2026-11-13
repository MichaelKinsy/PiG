package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Ports packages/chord/test/bench/state-binding.ts: benchmark of binding state-only references (decision "Events:
// fire-and-forget members": contracts without event members pay nothing for events per binding): a same-host reference
// got and released on a live local object; a remote reference got and released over a memory link (its hold, `bound`
// and `release`); and a remote reference rebound by its provider (`loading`, then a fresh `bound`, applied and
// acknowledged). Pi prints time and heap per step; the Go form is one sub-benchmark per row. The script asserts
// nothing: its rows are measurements only.
// Run by hand (not in CI): go test -run '^$' -bench BenchmarkStateBinding -benchtime=5000x ./chord/next/host/

// benchBindingCounter is a state-only local type: one count.
var benchBindingCounter = mustContract(contracts.Local("bench.counter", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
}))

// benchBindingView is a state-only remote type: one count.
var benchBindingView = mustContract(contracts.Remote("bench.view", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
	Variants: []contracts.VariantEntry{contracts.Var("v", contracts.VariantDeclaration{
		State: []contracts.Property{contracts.Prop("n", contracts.Int())},
	})},
}))

func benchBindingFacet() Facet {
	return DefineFacet(func(host FacetHost, _ FacetSelf) error {
		if err := host.Serve(benchBindingCounter, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
			}}, nil
		}, ServeOptions{}); err != nil {
			return err
		}
		return host.Serve(benchBindingView, func(*delta.JsonObject, data.Principal, Self) (contracts.Implementation, error) {
			return contracts.Implementation{Variant: "v", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"n": constant(1.0)},
			}}, nil
		}, ServeOptions{})
	})
}

func benchBindingServer(b *testing.B) Host {
	server := newHost(b, HostOptions{Address: new(Server.Address(nil)), Principal: serverPrincipal(), OnError: func(HostError) {}})
	mustLoad(b)(server.Load([]HostFacet{{Name: "@bench/app", Facet: benchBindingFacet()}}, background))
	return server
}

func BenchmarkStateBinding(b *testing.B) {
	// A same-host reference to a live local object, got and released.
	b.Run("same host: get and release", func(b *testing.B) {
		host := benchBindingServer(b)
		kept, err := host.Get(background, benchBindingCounter, jsonObject("id", "a"), GetOptions{})
		must(b, err)
		defer kept.Release()
		b.ReportAllocs()
		for b.Loop() {
			ref, err := host.Get(background, benchBindingCounter, jsonObject("id", "a"), GetOptions{})
			if err != nil {
				b.Fatal(err)
			}
			ref.Release()
		}
	})

	// A remote reference over a memory link, got and released (its object kept live by another reference).
	b.Run("over a link: get and release", func(b *testing.B) {
		server := benchBindingServer(b)
		client := newHost(b, HostOptions{OnError: func(HostError) {}})
		mustLoad(b)(client.Load(nil, background))
		x, y := link.MemoryLinkPair()
		must(b, server.Accept(x, LinkOptions{Principal: ownerPrincipal()}, background))
		must(b, client.Connect(y, LinkOptions{Principal: serverPrincipal()}, background))
		kept, err := client.Get(background, benchBindingView, jsonObject("id", "a"), GetOptions{})
		must(b, err)
		defer kept.Release()
		b.ReportAllocs()
		for b.Loop() {
			ref, err := client.Get(background, benchBindingView, jsonObject("id", "a"), GetOptions{})
			if err != nil {
				b.Fatal(err)
			}
			ref.Release()
		}
	})

	// A remote reference rebound by a raw provider: `loading`, then a fresh `bound`, each step awaited until acknowledged.
	b.Run("over a link: rebound (loading, bound)", func(b *testing.B) {
		client := newHost(b, HostOptions{OnError: func(HostError) {}})
		mustLoad(b)(client.Load(nil, background))
		x, y := link.MemoryLinkPair()
		must(b, client.Connect(x, LinkOptions{Principal: serverPrincipal(), Codec: link.BinaryCodec}, background))
		// The raw provider acknowledges nothing itself and reads the client's acks: `calls: 256` in its hello.
		peer := newRawPeer(b, y, rawPeerOptions{Codec: link.BinaryCodec, Calls: 256})
		got := make(chan *Reference, 1)
		go func() {
			ref, err := client.Get(background, benchBindingView, jsonObject("id", "a"), GetOptions{})
			if err != nil {
				b.Error(err)
			}
			got <- ref
		}()
		hold := peer.Next("hold").(*link.HoldMessage).Hold
		bound := func() {
			peer.Send(&link.BoundMessage{Holds: []link.BoundEntry{{
				Hold: hold, Variant: 0, Failed: []link.FailedMember{},
				Members: []link.BoundMember{{Member: 0, Value: link.Text("1")}},
			}}})
		}
		bound()
		view := <-got
		if view == nil {
			return
		}
		defer view.Release()
		peer.Next("ack")
		b.ReportAllocs()
		for b.Loop() {
			peer.Send(&link.LoadingMessage{Hold: hold})
			bound()
			peer.Next("ack")
		}
	})
}
