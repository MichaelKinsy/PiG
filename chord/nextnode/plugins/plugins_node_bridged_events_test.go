package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/internal/hostevents"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Not upstream's: the events of a Go core facet's object reach a plugin on the Node host through the facet's proxy
// (plugins_node_bridged.go, node/proxy.mjs, node/events.mjs). Each expectation is a link's, with the link's numbers
// (chord/next/host/remote_events_order_window_test.go) and Pi's source line.

// gmMatch is `test.gomatch`, a Go core facet's local type with an event member: `serve(sample)` emits the sample on
// `paddle`, then returns its y; `hold(n)` returns n once the probe releases it. The type has a param, and so has the
// plugin's below: the object of a local type without params lives as long as its host (Pi host/staging.ts:163
// `hostLifetime: contractData(contract).params.fields.length === 0`, host/object.ts:1356: kept whatever its holders), so
// no release would end it, and a release that ends the object is what the tests of the proxy's release and of the
// process's end observe.
var gmMatch = mustContract(contracts.Local("test.gomatch", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("paddle", contracts.Event(evSample)),
		contracts.Mem("serve", contracts.NewMethod(evSample, contracts.Num())),
		contracts.Mem("hold", contracts.NewMethod(contracts.Num(), contracts.Num())),
	},
}))

// gmLog is `test.matchlog`, what the plugin below serves: `play(y)` calls the Go object's `serve({ y })` and answers what
// the plugin saw so far, in order ("event <y>" per delivery, "result <y>" per returned call); `seen(0)` is that log;
// `stop(0)` ends the plugin's subscription and answers how many deliveries it got; `stamp(y)` calls `serve({ y })`, keeps
// its event loop busy for 100 ms before it reads anything, and answers the `at` its listener got for that event.
var gmLog = mustContract(contracts.Local("test.matchlog", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("play", contracts.NewMethod(contracts.Num(), contracts.Arr(contracts.Str()))),
		contracts.Mem("seen", contracts.NewMethod(contracts.Num(), contracts.Arr(contracts.Str()))),
		contracts.Mem("stop", contracts.NewMethod(contracts.Num(), contracts.Num())),
		contracts.Mem("stamp", contracts.NewMethod(contracts.Num(), contracts.Num())),
		contracts.Mem("flood", contracts.NewMethod(contracts.Num(), contracts.Num())),
	},
}))

// gmParams are the params of the one match of a test, and of the plugin's log of it.
func gmParams() *delta.JsonObject { return delta.JsonObjectOf("id", "m1") }

// gmClosed counts the closes of the Go objects' natives: the objects that ended.
var gmClosed atomic.Int32

// gmProbe is the sources of the Go objects served, the last one first asked for.
type gmProbe struct {
	mu      sync.Mutex
	paddles []contracts.EventSource[delta.JsonValue]
	// holding counts the calls of `hold` that are running; each returns its argument once release is closed.
	holding atomic.Int32
	release chan struct{}
}

func (p *gmProbe) paddle() contracts.EventSource[delta.JsonValue] {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paddles[len(p.paddles)-1]
}

// gmFacet is the Go core facet that serves `test.gomatch`.
func gmFacet(probe *gmProbe) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(gmMatch, func(_ *delta.JsonObject, self host.Self) (contracts.MembersImplementation, error) {
			paddle := self.Events()
			self.Open(func(context.Context) (any, error) { return struct{}{}, nil },
				func(any, host.NativeCloseReason, context.Context) error {
					gmClosed.Add(1)
					return nil
				})
			probe.mu.Lock()
			probe.paddles = append(probe.paddles, paddle)
			probe.mu.Unlock()
			return contracts.MembersImplementation{
				Events: map[string]contracts.EventMember[delta.JsonValue]{"paddle": paddle},
				Methods: map[string]contracts.MethodFunction{
					"serve": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
						if err := paddle.Emit(args); err != nil {
							return nil, err
						}
						return args.(*delta.JsonObject).Value("y"), nil
					},
					"hold": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						probe.holding.Add(1)
						// Pi: `await released.promise`. A Go handler runs in the section that admits its call until it parks
						// (chordctx.Suspend, host_loop_lend.go): one that waited without parking would keep the host's loop,
						// as a JavaScript method that never awaits keeps the thread, and no other call would start.
						chordctx.Suspend(ctx)
						select {
						case <-probe.release:
							return args, nil
						case <-ctx.Done():
							return nil, context.Cause(ctx)
						}
					},
				},
			}, nil
		}, host.ServeOptions{})
	})
}

// gmPlugin is a plugin that holds the Go object, subscribes to its `paddle` once live, and serves `test.matchlog`.
func gmPlugin(t *testing.T) PluginScript {
	t.Helper()
	source := `(function (require, module, exports) {
const { arr, defineFacet, event, local, method, num, obj, str } = require("@earendil-works/chord");
const Sample = obj({ y: num() });
const Match = local("test.gomatch", { version: 1, params: { id: str() }, members: { paddle: event(Sample), serve: method(Sample, num()), hold: method(num(), num()) } });
const Log = local("test.matchlog", { version: 1, params: { id: str() }, members: {
	play: method(num(), arr(str())), seen: method(num(), arr(str())), stop: method(num(), num()), stamp: method(num(), num()),
	flood: method(num(), num()),
} });
module.exports = { default: defineFacet((host) => host.serve(Log, (params, self) => {
	const match = self.get(Match, { id: params.id });
	const log = [];
	const stamps = new Map();
	const subscribed = Promise.withResolvers();
	self.onLive(async (ctx) => {
		// Subscribed as soon as the Go object is bound: what it emits from now on is this listener's.
		subscribed.resolve(await match.paddle.on(({ value, at }) => {
			log.push("event " + value.y);
			stamps.set(value.y, at);
		}, ctx));
	});
	return {
		play: async (y, ctx) => {
			await subscribed.promise;
			const result = await match.serve({ y }, ctx);
			// The event emitted before the result was delivered before the call returned: nothing else was awaited in between.
			log.push("result " + result);
			return log;
		},
		seen: async () => log,
		stop: async () => {
			(await subscribed.promise).stop();
			return stamps.size;
		},
		stamp: async (y, ctx) => {
			await subscribed.promise;
			const pending = match.serve({ y }, ctx);
			// The Go host emits while this loop keeps the event unread: it is read well after its stamp.
			const until = Date.now() + 100;
			while (Date.now() < until);
			await pending;
			return stamps.get(y);
		},
		// Every call is made before any is awaited: they are in flight together.
		flood: async (count, ctx) => {
			await subscribed.promise;
			return (await Promise.all(Array.from({ length: count }, (_, index) => match.hold(index, ctx)))).length;
		},
	};
})) };
})`
	return handmade(t, "@test/matchlog", "worker", source)
}

// An event the Go object emits before a call's result reaches the plugin's listener before that call returns, and one
// emitted after it after it, as on one link (TestEventsKeepTheirOrderAgainstACallsResultOnOneLink; Pi
// remote/provider.ts:382-391: `sendEvents` sends at the flush, before the call's reply).
func TestBridgedEventsOfAGoObjectReachThePluginsListenerInOrderAgainstACallsResult(t *testing.T) {
	probe := &gmProbe{release: make(chan struct{})}
	goHost := bgGoHost(t, host.HostFacet{Name: "@app/match", Facet: gmFacet(probe)})
	node := bgNode(t, []BridgedFacet{{Name: "@app/match", Host: goHost, Contracts: []*contracts.Contract{gmMatch}}}, gmPlugin(t))
	out, err := node.Get(background, gmLog, gmParams(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	// The sender is slowed: an answer that did not wait for what its call emitted would be written first.
	write := node.events.control
	node.events.control = func(message any) error {
		time.Sleep(50 * time.Millisecond)
		return write(message)
	}
	played, err := out.Call("play", 1.0, background)
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{"event 1", "result 1"}; !reflect.DeepEqual(played, want) {
		t.Fatalf("the plugin saw %v when its call returned, want %v", played, want)
	}
	if err := probe.paddle().Emit(delta.JsonObjectOf("y", 2.0)); err != nil {
		t.Fatal(err)
	}
	var seen delta.JsonValue
	eventually(t, 15*time.Second, "the event emitted after the call to reach the plugin", func() bool {
		seen, err = out.Call("seen", 0.0, background)
		if err != nil {
			t.Fatal(err)
		}
		return len(seen.([]any)) == 3
	})
	if want := []any{"event 1", "result 1", "event 2"}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("the plugin saw %v, want %v", seen, want)
	}
}

// gmRig is a Go host serving `test.gomatch` with its telemetry recorded, a Node host whose plugin holds the Go object
// through the facet's proxy, and the plugin's log held.
type gmRig struct {
	probe    *gmProbe
	recorder *telemetry.InMemoryTelemetryContext
	node     *NodeHost
	out      *NodeReference
}

func gmOpen(t *testing.T, options host.HostOptions) *gmRig {
	t.Helper()
	rig := &gmRig{probe: &gmProbe{release: make(chan struct{})}, recorder: telemetry.NewInMemoryTelemetryContext()}
	options.Principal, options.OnError, options.Telemetry = serverPrincipal, func(host.HostError) {}, rig.recorder
	goHost, err := host.CreateHost(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = goHost.Close(background) })
	if _, err := goHost.Load([]host.HostFacet{{Name: "@app/match", Facet: gmFacet(rig.probe)}}, background); err != nil {
		t.Fatal(err)
	}
	rig.node = bgNode(t, []BridgedFacet{{Name: "@app/match", Host: goHost, Contracts: []*contracts.Contract{gmMatch}}}, gmPlugin(t))
	rig.out, err = rig.node.Get(background, gmLog, gmParams(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rig.out.Release)
	return rig
}

// emitTurns emits y from..to on the Go object's paddle, each in a turn of its own (one message per emit), and waits
// until the sender wrote what they queued.
func (r *gmRig) emitTurns(t *testing.T, from, to int) {
	t.Helper()
	for y := from; y <= to; y++ {
		if err := r.probe.paddle().Emit(delta.JsonObjectOf("y", float64(y))); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.node.events.flush(background); err != nil {
		t.Fatal(err)
	}
}

// counts are the Go host's counters by name, `.reason` appended when a measurement has one.
func (r *gmRig) counts() map[string]float64 {
	result := map[string]float64{}
	for _, measurement := range r.recorder.GetMetrics() {
		key := measurement.Name
		if reason, ok := measurement.Attributes.Get("reason"); ok {
			key += "." + fmt.Sprint(reason)
		}
		result[key] += measurement.Value
	}
	return result
}

// consume makes the test the consumer of the Go side's events: what the sender would write is recorded and not written,
// so the Node host gets no event and acknowledges none; ack is an `ack` message as the Node host sends it.
func (r *gmRig) consume() *wireChannel {
	channel := &wireChannel{}
	r.node.events.control = channel.control
	return channel
}

func (r *gmRig) ack(messages int) {
	r.node.control(fmt.Appendf(nil, `{"event":"ack","events":%d}`, messages))
}

// wireYs is the ys of the `event` messages among written, per message.
func wireYs(t *testing.T, written []string) [][]float64 {
	t.Helper()
	ys := [][]float64{}
	for _, text := range written {
		var message eventsMessage
		if err := json.Unmarshal([]byte(text), &message); err != nil {
			t.Fatal(err)
		}
		if message.Op != "event" {
			continue
		}
		each := []float64{}
		for _, entry := range message.Events {
			var value struct{ Y float64 }
			if err := json.Unmarshal([]byte(entry.Value), &value); err != nil {
				t.Fatal(err)
			}
			each = append(each, value.Y)
		}
		ys = append(ys, each)
	}
	return ys
}

// The Go host's event window bounds what the Node host has not acknowledged, with the numbers of
// TestTheDefaultEventWindowSendsThe32ndUnacknowledgedMessageAndDropsThe33rd: the reference the proxy holds is tapped
// when it binds, the 32nd unacknowledged message is written, the 33rd is dropped at the Go host and counted
// `chord.events.dropped{reason=window}` (Pi remote/provider.ts:369-372), and one ack frees one slot.
func TestBridgedTheGoHostWritesThe32ndUnacknowledgedMessageAndDropsThe33rd(t *testing.T) {
	rig := gmOpen(t, host.HostOptions{})
	channel := rig.consume()
	rig.emitTurns(t, 1, 32)
	if got := len(channel.all()); got != 32 {
		t.Fatalf("messages with 32 outstanding = %d, want 32 (the window is 32)", got)
	}
	if got := rig.counts()["chord.events.dropped.window"]; got != 0 {
		t.Fatalf("dropped at 32 outstanding = %v, want 0", got)
	}
	rig.emitTurns(t, 33, 33)
	if got := wireYs(t, channel.all()[32:]); len(got) != 0 {
		t.Fatalf("the 33rd outstanding event was written: %v, want it dropped", got)
	}
	counts := rig.counts()
	for name, want := range map[string]float64{
		"chord.events.emitted": 33, "chord.events.sent": 32, "chord.events.dropped.window": 1, "chord.events.encodes": 32,
	} {
		if counts[name] != want {
			t.Fatalf("%s = %v, want %v (all %v)", name, counts[name], want, counts)
		}
	}
	rig.ack(1)
	rig.emitTurns(t, 34, 35)
	if got, want := wireYs(t, channel.all()[32:]), [][]float64{{34}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ys after one ack = %v, want %v", got, want)
	}
}

// An ack of the Node host that counts messages never written, or a message a second time, frees only what is
// unacknowledged: the Go host's window never grows (Pi remote/provider.ts:681-684), with the numbers of
// TestAnEventAckForMessagesNeverSentOrCountedTwiceIsIgnoredAndOpensNoSlot.
func TestBridgedAnAckForMessagesNeverWrittenOrCountedTwiceIsIgnoredAndOpensNoSlot(t *testing.T) {
	open := func(t *testing.T) (*gmRig, *wireChannel) {
		rig := gmOpen(t, host.HostOptions{EventWindow: new(2)})
		return rig, rig.consume()
	}
	t.Run("an ack for five messages when none was written: the window stays 2", func(t *testing.T) {
		rig, channel := open(t)
		rig.ack(5)
		rig.emitTurns(t, 1, 4)
		if got, want := wireYs(t, channel.all()), [][]float64{{1}, {2}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ys = %v, want %v", got, want)
		}
	})
	t.Run("an ack counting more than is outstanding frees only what is outstanding", func(t *testing.T) {
		rig, channel := open(t)
		rig.emitTurns(t, 1, 1)
		rig.ack(3)
		rig.emitTurns(t, 2, 5)
		if got, want := wireYs(t, channel.all()), [][]float64{{1}, {2}, {3}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ys = %v, want %v", got, want)
		}
	})
	t.Run("the same message acknowledged twice frees one slot", func(t *testing.T) {
		rig, channel := open(t)
		rig.emitTurns(t, 1, 1)
		rig.ack(1)
		rig.ack(1)
		rig.emitTurns(t, 2, 5)
		if got, want := wireYs(t, channel.all()), [][]float64{{1}, {2}, {3}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("ys = %v, want %v", got, want)
		}
	})
}

// settled waits until the Node host acknowledged every message the Go side wrote.
func (r *gmRig) settled(t *testing.T) {
	t.Helper()
	eventually(t, 15*time.Second, "the Node host to acknowledge what was written", func() bool {
		r.node.events.mu.Lock()
		defer r.node.events.mu.Unlock()
		return len(r.node.events.tickets) == 0 && len(r.node.events.queue) == 0 && !r.node.events.sending
	})
}

func (r *gmRig) call(t *testing.T, method string, argument float64) delta.JsonValue {
	t.Helper()
	result, err := r.out.Call(method, argument, background)
	if err != nil {
		t.Fatalf("%s(%v): %v", method, argument, err)
	}
	return result
}

// A plugin that stops its subscription mid stream gets nothing more, and the Go object's events are still written and
// acknowledged: the reference is tapped at its bind, not at a listener (decision "subscribe at bind": a link sends a
// hold's events whether its consumer listens or not, and its consumer acknowledges every message, Pi
// remote/consumer.ts:312-320). Forty events with no listener, one at a time, never fill the window of 32.
func TestBridgedAPluginThatStopsListeningGetsNothingMoreAndTheEventsAreStillWrittenAndAcknowledged(t *testing.T) {
	rig := gmOpen(t, host.HostOptions{})
	if got, want := rig.call(t, "play", 1), []any{"event 1", "result 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the plugin saw %v, want %v", got, want)
	}
	rig.emitTurns(t, 2, 2)
	eventually(t, 15*time.Second, "the second event to reach the plugin", func() bool { return len(rig.call(t, "seen", 0).([]any)) == 3 })
	if got := rig.call(t, "stop", 0); got != 2.0 {
		t.Fatalf("deliveries before the stop = %v, want 2", got)
	}
	for y := 3; y <= 42; y++ {
		rig.emitTurns(t, y, y)
		rig.settled(t)
	}
	if got, want := rig.call(t, "seen", 0), []any{"event 1", "result 1", "event 2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the plugin saw %v after it stopped listening, want %v", got, want)
	}
	counts := rig.counts()
	for name, want := range map[string]float64{"chord.events.emitted": 42, "chord.events.sent": 42, "chord.events.dropped.window": 0} {
		if counts[name] != want {
			t.Fatalf("%s = %v, want %v (all %v)", name, counts[name], want, counts)
		}
	}
}

// The `at` a plugin's listener gets is the Go host's stamp of the emit, as over a link (value and stamp unchanged: Pi
// remote/remote-object.ts:547, `events.push(node, parsePart(json), at)`), never the time the event was read: the plugin
// keeps its loop busy for 100 ms between the call that makes the Go object emit and the read of the event.
func TestBridgedThePluginsListenerGetsTheGoHostsStampNotTheTimeTheEventArrived(t *testing.T) {
	rig := gmOpen(t, host.HostOptions{})
	goHost := rig.node.bridge.bridged[0].Host
	held, err := goHost.Get(background, gmMatch, gmParams(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(held.Release)
	var mu sync.Mutex
	var emitted []int64
	if _, err := held.Events("paddle").On(func(delivery contracts.EventDelivery[delta.JsonValue]) {
		mu.Lock()
		defer mu.Unlock()
		emitted = append(emitted, delivery.At)
	}, background); err != nil {
		t.Fatal(err)
	}
	got := rig.call(t, "stamp", 7)
	mu.Lock()
	defer mu.Unlock()
	if len(emitted) != 1 || got != float64(emitted[0]) {
		t.Fatalf("the plugin's listener got at = %v, the Go host stamped the emit %v", got, emitted)
	}
}

// A process that ends with events written and unacknowledged leaves nothing behind: no goroutine of the sender when
// Exited closes, the Go references the proxies held released (the object's native closes), and the Go host's port closed
// (an object another reference keeps alive emits to nobody).
func TestBridgedTheProcessEndingWithEventsOutstandingLeavesNoSenderAndNoTapOnTheGoHost(t *testing.T) {
	for _, mode := range []string{"close", "kill"} {
		t.Run(mode, func(t *testing.T) {
			gmClosed.Store(0)
			rig := gmOpen(t, host.HostOptions{})
			if got, want := rig.call(t, "play", 1), []any{"event 1", "result 1"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("the plugin saw %v, want %v", got, want)
			}
			port, _ := rig.tap(t)
			for y := 2; y <= 20; y++ {
				if err := rig.probe.paddle().Emit(delta.JsonObjectOf("y", float64(y))); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "kill" {
				rig.node.kill()
			} else {
				_ = rig.node.Close(background)
			}
			<-rig.node.Exited()
			if stacks := senderStacks(); stacks != "" {
				t.Fatalf("a goroutine of the sender outlived the process:\n%s", stacks)
			}
			eventually(t, 15*time.Second, "the proxy's Go reference to be released", func() bool { return gmClosed.Load() == 1 })
			// A new object, held by the Go side alone: the port of the Node host that ended takes nothing of it.
			goHost := rig.node.bridge.bridged[0].Host
			held, err := goHost.Get(background, gmMatch, gmParams(), host.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(held.Release)
			sent := rig.counts()["chord.events.sent"]
			if err := rig.probe.paddle().Emit(delta.JsonObjectOf("y", 99.0)); err != nil {
				t.Fatal(err)
			}
			if got := rig.counts()["chord.events.sent"]; got != sent {
				t.Fatalf("chord.events.sent went from %v to %v after the process ended, want nothing sent", sent, got)
			}
			if _, err := port.Tap(held, 1); err == nil || err.Error() != "hostevents: the port is closed" {
				t.Fatalf("Tap on the port of the Node host that ended = %v, want the port closed", err)
			}
		})
	}
}

// The bridge has no call window, as a trusted link has none (Pi remote/host-link.ts:83: `const calls = trusted ? undefined
// : host.callWindow`; remote/provider.ts:529: "No window announced (a trusted link): no count closes the link"; decision
// "Call windows only for untrusted links"): 10,001 calls of a plugin to a Go object are in flight together, one more than
// the default window of a link that is not trusted (TestTheDefaultCallWindowRuns10000ConcurrentCallsAndClosesTheLinkAtThe10001st),
// and every one completes. While they are in flight the channel is not held in either direction: an event of the Go
// object reaches the plugin's listener (the Go side's sender writes, the Node host reads) and a call of the plugin is
// answered (the Node host writes, the Go side's reader reads).
func TestBridged10001ConcurrentCallsOfAPluginToAGoObjectRunAndComplete(t *testing.T) {
	rig := gmOpen(t, host.HostOptions{})
	const calls = 10_001
	flooded := make(chan delta.JsonValue, 1)
	failed := make(chan error, 1)
	go func() {
		result, err := rig.out.Call("flood", float64(calls), background)
		if err != nil {
			failed <- err
			return
		}
		flooded <- result
	}()
	deadline := time.After(120 * time.Second)
	for rig.probe.holding.Load() != calls {
		select {
		case err := <-failed:
			t.Fatalf("flood: %v with %d calls running", err, rig.probe.holding.Load())
		case <-deadline:
			t.Fatalf("%d calls run on the Go object, want %d in flight together", rig.probe.holding.Load(), calls)
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := rig.probe.paddle().Emit(delta.JsonObjectOf("y", 5.0)); err != nil {
		t.Fatal(err)
	}
	eventually(t, 15*time.Second, "an event emitted with 10,001 calls in flight to reach the plugin", func() bool {
		return reflect.DeepEqual(rig.call(t, "seen", 0), []any{"event 5"})
	})
	if got := rig.probe.holding.Load(); got != calls {
		t.Fatalf("%d calls run on the Go object after the event and the call, want %d still in flight", got, calls)
	}
	close(rig.probe.release)
	select {
	case got := <-flooded:
		if got != float64(calls) {
			t.Fatalf("flood = %v, want %d completed calls", got, calls)
		}
	case err := <-failed:
		t.Fatalf("flood: %v", err)
	case <-time.After(120 * time.Second):
		t.Fatal("the calls did not complete")
	}
}

// tlServer is the host kind of the Go server below, and tlEcho a remote type it serves on its address.
var tlServer = mustKind(contracts.NewHostKind("test.goserver"))

var tlEcho = mustContract(contracts.Remote("test.goecho", contracts.RemoteDeclaration{
	Version: 1, Params: contracts.Params{},
	HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return tlServer.Address(nil), nil },
	Methods: []contracts.RemoteMethodEntry{contracts.Meth("echo", contracts.NewMethod(contracts.Num(), contracts.Num()))},
}))

// tlRelay is `test.relay`, served by the plugin below: `relay(n)` calls the Go server's `echo(n)` over the Node host's link.
var tlRelay = mustContract(contracts.Local("test.relay", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("relay", contracts.NewMethod(contracts.Num(), contracts.Num()))},
}))

// A plugin's call of a remote type the Go host serves goes over NodeHost.Link(), which the Go host accepted as trusted:
// its hello announces no call window (Pi remote/host-link.ts:83), the Node host's consumer therefore limits nothing
// (Pi remote/calls.ts:11: "a peer that announced none (a trusted link) gets every call at once"), and the call completes.
func TestAPluginsCallOfATypeTheGoHostServesOverTheNodeHostsTrustedLinkCompletes(t *testing.T) {
	address := tlServer.Address(nil)
	server, err := host.CreateHost(host.HostOptions{Address: &address, Principal: serverPrincipal, OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close(background) })
	facet := host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(tlEcho, func(*delta.JsonObject, data.Principal, host.Self) (contracts.MembersImplementation, error) {
			return contracts.MembersImplementation{
				Methods: map[string]contracts.MethodFunction{
					"echo": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) { return args, nil },
				},
			}, nil
		}, host.ServeOptions{})
	})
	if _, err := server.Load([]host.HostFacet{{Name: "@app/echo", Facet: facet}}, background); err != nil {
		t.Fatal(err)
	}
	plugin := handmade(t, "@test/relay", "server", `(function (require, module, exports) {
const { defineFacet, hostKind, local, method, num, remote } = require("@earendil-works/chord");
const Server = hostKind("test.goserver");
const Echo = remote("test.goecho", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: {}, methods: { echo: method(num(), num()) } } } });
const Relay = local("test.relay", { version: 1, params: {}, members: { relay: method(num(), num()) } });
module.exports = { default: defineFacet((host) => host.serve(Relay, (_params, self) => {
	const echo = self.get(Echo, {});
	return { relay: async (n, ctx) => echo.echo(n, ctx) };
})) };
})`)
	node, report, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "server", Scripts: []PluginScript{plugin}, Address: &address, Principal: serverPrincipal, Peer: serverPrincipal,
		ParentServes: server.Serves,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close(background) })
	if len(report.Failed) != 0 {
		t.Fatalf("failed = %+v", report.Failed)
	}
	if err := server.Accept(node.Link(), host.LinkOptions{Principal: serverPrincipal, Trusted: true}, background); err != nil {
		t.Fatal(err)
	}
	relay, err := node.Get(background, tlRelay, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(relay.Release)
	done := make(chan struct{})
	var got delta.JsonValue
	go func() {
		defer close(done)
		got, err = relay.Call("relay", 7.0, background)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the plugin's call over the trusted link did not complete after 30s")
	}
	if err != nil || got != 7.0 {
		t.Fatalf("relay = %v, %v, want 7", got, err)
	}
}

// tap is the Go host's port the proxy's reference is tapped on, and the handle it is tapped under.
func (r *gmRig) tap(t *testing.T) (hostevents.Port, int) {
	t.Helper()
	serving := r.node.serving()
	serving.mu.Lock()
	defer serving.mu.Unlock()
	if len(serving.tapped) != 1 {
		t.Fatalf("%d references are tapped, want the proxy's one", len(serving.tapped))
	}
	for handle, port := range serving.tapped {
		return port, handle
	}
	return nil, 0
}

// The proxy's release untaps its reference on the Go host's port before it releases it: the port keeps no hold of a
// reference the Node host let go (its handle can be tapped again), as a hold that ends over a link takes its taps with it.
func TestBridgedAProxysReleaseUntapsItsReferenceOnTheGoHostsPort(t *testing.T) {
	rig := gmOpen(t, host.HostOptions{})
	port, handle := rig.tap(t)
	held, err := rig.node.bridge.bridged[0].Host.Get(background, gmMatch, gmParams(), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(held.Release)
	if _, err := port.Tap(held, handle); err == nil || err.Error() != fmt.Sprintf("hostevents: hold %d is tapped", handle) {
		t.Fatalf("Tap under the proxy's handle = %v while the proxy holds it, want the hold in use", err)
	}
	rig.out.Release()
	serving := rig.node.serving()
	eventually(t, 15*time.Second, "the proxy to release its Go reference", func() bool {
		serving.mu.Lock()
		defer serving.mu.Unlock()
		return len(serving.references) == 0
	})
	if _, err := port.Tap(held, handle); err != nil {
		t.Fatalf("Tap under the handle the proxy released = %v, want it free: the release did not untap it", err)
	}
	port.Untap(handle)
}
