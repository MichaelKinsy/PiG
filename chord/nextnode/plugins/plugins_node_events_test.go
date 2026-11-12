package plugins

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's: event members (Pi 8d30e6306, decision "Events: fire-and-forget members") at the boundary between a Go
// host and a Node host. Oracle: Pi's own contracts and host, which the Node host runs (the vendored tree carries
// 8d30e6306): a plugin declares an event member with the local marker `event(...)`, emits from `self.events()` and
// subscribes with a reference's `on`, as packages/chord/test/host/events.test.ts does in process.

// evModule is a core facet serving `test.ticker`, a local type with an event member, and `test.listener`, which holds the
// ticker, subscribes to its `ticks` once live and tells what it got: `emit(y)` emits a sample on the ticker's source,
// `seen(0)` is every y its listener was called with, in order.
const evModule = `import { arr, constant, defineFacet, event, int, local, method, num, obj } from "@earendil-works/chord";
const Sample = obj({ y: num() });
const Ticker = local("test.ticker", { version: 1, params: {}, members: { ticks: event(Sample), count: int() } });
const Listener = local("test.listener", {
	version: 1,
	params: {},
	members: { emit: method(num()), seen: method(int(), arr(num())) },
});
const sources = [];
export default defineFacet((host) => {
	host.serve(Ticker, (_params, self) => {
		const ticks = self.events();
		sources.push(ticks);
		return { ticks, count: constant(0) };
	});
	host.serve(Listener, (_params, self) => {
		const ticker = self.get(Ticker, {});
		const seen = [];
		const subscribed = Promise.withResolvers();
		self.onLive(async (ctx) => {
			await ticker.ticks.on(({ value }) => void seen.push(value.y), ctx);
			subscribed.resolve();
		});
		return {
			emit: async (y) => {
				await subscribed.promise;
				sources.at(-1).emit({ y });
			},
			seen: async () => seen,
		};
	});
});
`

var evSample = contracts.Obj(contracts.Prop("y", contracts.Num()))

// evTicker is the Go declaration of `test.ticker`: the same members, so the same fingerprint.
var evTicker = mustContract(contracts.Local("test.ticker", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("ticks", contracts.Event(evSample)), contracts.Mem("count", contracts.Int())},
}))

var evListener = mustContract(contracts.Local("test.listener", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("emit", contracts.NewMethod(contracts.Num())),
		contracts.Mem("seen", contracts.NewMethod(contracts.Int(), contracts.Arr(contracts.Num()))),
	},
}))

// evNode starts a Node host that serves the ticker and its listener from a core facet.
func evNode(t *testing.T) *NodeHost {
	t.Helper()
	file := filepath.Join(t.TempDir(), "ticker.mjs")
	if err := os.WriteFile(file, []byte(evModule), 0o644); err != nil {
		t.Fatal(err)
	}
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "server", Core: []CoreFacet{{Name: "@test/ticker", Module: file}}, Principal: serverPrincipal, Peer: serverPrincipal,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	acceptorOf(t, node, serverPrincipal)
	return node
}

// A plugin on the Node host declares an event member on a local type, emits it, and another object of the same host that
// subscribed gets every emit in order (events.test.ts "delivers a turn's emits in one flush ..., in order"): the type is
// whole inside the Node host.
func TestANodePluginDeclaresAnEventMemberAndItsEmitsReachASubscriberOnTheNodeHost(t *testing.T) {
	node := evNode(t)
	listener, err := node.Get(background, evListener, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(listener.Release)
	for _, y := range []float64{1, 2.5, 3} {
		if _, err := listener.Call("emit", y, background); err != nil {
			t.Fatalf("emit(%v): %v", y, err)
		}
	}
	seen, err := listener.Call("seen", 0.0, background)
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{1.0, 2.5, 3.0}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("seen = %v, want %v", seen, want)
	}
}
