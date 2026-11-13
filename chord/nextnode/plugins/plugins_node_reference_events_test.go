package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Not upstream's: the events of an object a Node host's plugins serve reach the Go side that holds it (NodeReference's
// event members, plugins_node_reference_events.go; node/bridge.mjs `get`, node/events.mjs `tap`). Each expectation is a
// link's, with the link's numbers (chord/next/host/remote_events_order_window_test.go) and Pi's source line. The other
// direction is plugins_node_bridged_events_test.go.

// ctModule is a core facet serving `test.court`, a local type with an event member. `serve(y)` emits { y } and returns y;
// `burst(n)` emits n samples, numbered on from the object's last, each in a turn of its own (one flush, one message
// each), and returns the last number; `stamped(y)` emits { y } while `Date.now()` answers 1700000000123; `rally(y)` emits
// { y }, and the object, which listens to its own member, emits { y: y + 1 } inside the flush that delivers it. The type
// has a param: its object ends with its last reference (a type without params is a host-lifetime object, Pi
// host/staging.ts:163).
const ctModule = `import { defineFacet, event, local, method, num, obj, str } from "@earendil-works/chord";
const Sample = obj({ y: num() });
const Court = local("test.court", { version: 1, params: { id: str() }, members: {
	paddle: event(Sample),
	serve: method(num(), num()),
	burst: method(num(), num()),
	stamped: method(num(), num()),
	rally: method(num(), num()),
} });
export default defineFacet((host) => {
	host.serve(Court, (_params, self) => {
		const paddle = self.events();
		let last = 0;
		let rallying;
		const listening = Promise.withResolvers();
		self.onLive(async (ctx) => {
			listening.resolve(await paddle.on(({ value }) => {
				if (value.y !== rallying) return;
				rallying = undefined;
				paddle.emit({ y: value.y + 1 });
			}, ctx));
		});
		return {
			paddle,
			serve: async (y) => {
				paddle.emit({ y });
				return y;
			},
			burst: async (count) => {
				for (let sent = 0; sent < count; sent++) {
					paddle.emit({ y: ++last });
					await new Promise((resolve) => setImmediate(resolve));
				}
				return last;
			},
			stamped: async (y) => {
				const now = Date.now;
				Date.now = () => 1700000000123;
				try {
					paddle.emit({ y });
				} finally {
					Date.now = now;
				}
				return y;
			},
			rally: async (y) => {
				await listening.promise;
				rallying = y;
				paddle.emit({ y });
				return y;
			},
		};
	});
});
`

// ctCourt is the Go declaration of `test.court`: the same members, so the same fingerprint.
var ctCourt = mustContract(contracts.Local("test.court", contracts.LocalDeclaration{
	Version: 1, Params: contracts.Params{contracts.Prop("id", contracts.Str())},
	Members: []contracts.LocalEntry{
		contracts.Mem("paddle", contracts.Event(evSample)),
		contracts.Mem("serve", contracts.NewMethod(contracts.Num(), contracts.Num())),
		contracts.Mem("burst", contracts.NewMethod(contracts.Num(), contracts.Num())),
		contracts.Mem("stamped", contracts.NewMethod(contracts.Num(), contracts.Num())),
		contracts.Mem("rally", contracts.NewMethod(contracts.Num(), contracts.Num())),
	},
}))

// ctRig is a Node host that serves the court, with its telemetry recorded.
type ctRig struct {
	node     *NodeHost
	recorder *telemetry.InMemoryTelemetryContext
}

// ctOpen starts the Node host; window is its event window (nil: the default, 32). The caller closes it.
func ctOpen(t *testing.T, window *int) *ctRig {
	t.Helper()
	file := filepath.Join(t.TempDir(), "court.mjs")
	if err := os.WriteFile(file, []byte(ctModule), 0o644); err != nil {
		t.Fatal(err)
	}
	rig := &ctRig{recorder: telemetry.NewInMemoryTelemetryContext()}
	node, _, err := NewPluginRuntime(NodeOptions{}).Load(RuntimeLoad{
		Kind: "server", Core: []CoreFacet{{Name: "@test/court", Module: file}}, Principal: serverPrincipal, Peer: serverPrincipal,
		Telemetry: rig.recorder, EventWindow: window,
	}, background)
	if err != nil {
		t.Fatal(err)
	}
	rig.node = node
	return rig
}

// ctStart is ctOpen closed when the test ends.
func ctStart(t *testing.T, window *int) *ctRig {
	t.Helper()
	rig := ctOpen(t, window)
	t.Cleanup(func() { _ = rig.node.Close(background) })
	return rig
}

// court holds the one court of a test.
func (r *ctRig) court(t *testing.T) *NodeReference {
	t.Helper()
	ref, err := r.node.Get(background, ctCourt, delta.JsonObjectOf("id", "c1"), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ref.Release)
	return ref
}

// counts are the Node host's counters by name, `.reason` appended when a measurement has one.
func (r *ctRig) counts() map[string]float64 {
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

// counted waits until the Node host's counters are want (they arrive on the channel, in the order the host made them).
func (r *ctRig) counted(t *testing.T, want map[string]float64) {
	t.Helper()
	matches := func() bool {
		counts := r.counts()
		for name, value := range want {
			if counts[name] != value {
				return false
			}
		}
		return true
	}
	deadline := time.Now().Add(15 * time.Second)
	for !matches() {
		if time.Now().After(deadline) {
			t.Fatalf("the Node host's counters = %v, want %v", r.counts(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// holdAcks makes the Go side acknowledge nothing: what its sender would write is recorded and not written, so the Node
// host's window frees only by ack, an `ack` message written to the Node host as the Go side writes one.
func (r *ctRig) holdAcks() *wireChannel {
	channel := &wireChannel{}
	r.node.events.control = channel.control
	return channel
}

func (r *ctRig) ack(t *testing.T, messages int) {
	t.Helper()
	if err := r.node.controlEvents(ackMessage{Op: "ack", Events: messages}); err != nil {
		t.Fatal(err)
	}
}

// acknowledged waits until the Go side wrote the acknowledgements of the messages it got so far: a request written after
// it reaches the Node host after them.
func (r *ctRig) acknowledged(t *testing.T) {
	t.Helper()
	if err := r.node.events.flush(background); err != nil {
		t.Fatal(err)
	}
}

func ctCall(t *testing.T, ref *NodeReference, method string, argument float64) delta.JsonValue {
	t.Helper()
	result, err := ref.Call(method, argument, background)
	if err != nil {
		t.Fatalf("%s(%v): %v", method, argument, err)
	}
	return result
}

// ctSeen is what a Go listener got: the ys and the stamps, in order.
type ctSeen struct {
	mu  sync.Mutex
	ys  []float64
	ats []int64
}

func (s *ctSeen) listener(delivery contracts.EventDelivery[delta.JsonValue]) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ys = append(s.ys, delivery.Value.(*delta.JsonObject).Value("y").(float64))
	s.ats = append(s.ats, delivery.At)
}

func (s *ctSeen) got() []float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]float64{}, s.ys...)
}

// ctListen subscribes a recording listener to the court's paddle under ctx.
func ctListen(t *testing.T, ref *NodeReference, ctx context.Context) (*ctSeen, contracts.EventSubscription) {
	t.Helper()
	seen := &ctSeen{}
	sub, err := ref.Events("paddle").On(seen.listener, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return seen, sub
}

func ctRange(from, to int) []float64 {
	ys := []float64{}
	for y := from; y <= to; y++ {
		ys = append(ys, float64(y))
	}
	return ys
}

// An event a plugin's object emits before a call's result reaches the Go listener before that call returns, and one
// emitted after it after it, as on one link (TestEventsKeepTheirOrderAgainstACallsResultOnOneLink; Pi
// remote/provider.ts:382-391: `sendEvents` sends at the flush, before the call's reply): the listener is on as soon as On
// returned, and the emit that follows at once is its.
func TestEventsOfANodeObjectReachTheGoListenerInOrderAgainstACallsResult(t *testing.T) {
	rig := ctStart(t, nil)
	ref := rig.court(t)
	var mu sync.Mutex
	var log []string
	record := func(line string) []string {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, line)
		return append([]string{}, log...)
	}
	if _, err := ref.Events("paddle").On(func(delivery contracts.EventDelivery[delta.JsonValue]) {
		record(fmt.Sprint("event ", delivery.Value.(*delta.JsonObject).Value("y")))
	}, background); err != nil {
		t.Fatal(err)
	}
	if got, want := record(fmt.Sprint("result ", ctCall(t, ref, "serve", 1))), []string{"event 1", "result 1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the Go side saw %v when its call returned, want %v", got, want)
	}
	if got, want := record(fmt.Sprint("result ", ctCall(t, ref, "serve", 2))), []string{"event 1", "result 1", "event 2", "result 2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the Go side saw %v, want %v", got, want)
	}
}

// The Node host's event window bounds what the Go side has not acknowledged, with the numbers of
// TestTheDefaultEventWindowSendsThe32ndUnacknowledgedMessageAndDropsThe33rd: the 32nd unacknowledged message is written,
// the 33rd is dropped at the Node host and counted `chord.events.dropped{reason=window}` (Pi remote/provider.ts:369-372),
// and one ack frees one slot. The Go side acknowledges each message it got, once, by count.
func TestTheNodeHostWritesThe32ndUnacknowledgedMessageAndDropsThe33rdAndOneAckFreesOne(t *testing.T) {
	rig := ctStart(t, nil)
	ref := rig.court(t)
	seen, _ := ctListen(t, ref, background)
	acks := rig.holdAcks()
	if got := ctCall(t, ref, "burst", 33); got != 33.0 {
		t.Fatalf("burst = %v, want 33 emitted", got)
	}
	if got, want := seen.got(), ctRange(1, 32); !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %v, want %v: the 33rd unacknowledged message is dropped", got, want)
	}
	rig.counted(t, map[string]float64{"chord.events.sent": 32, "chord.events.dropped.window": 1})
	rig.acknowledged(t)
	want := []string{}
	for range 32 {
		want = append(want, `{"op":"ack","events":1}`)
	}
	if got := acks.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("the Go side's acknowledgements = %q, want one per message, 32", got)
	}
	rig.ack(t, 1)
	if got := ctCall(t, ref, "burst", 2); got != 35.0 {
		t.Fatalf("burst = %v, want 35 emitted", got)
	}
	if got, want := seen.got(), append(ctRange(1, 32), 34); !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %v after one ack, want %v: one ack frees one slot", got, want)
	}
	rig.counted(t, map[string]float64{"chord.events.sent": 33, "chord.events.dropped.window": 2})
}

// An ack of the Go side that counts messages never written, or a message a second time, frees only what is
// unacknowledged: the Node host's window never grows (Pi remote/provider.ts:681-684), with the numbers of
// TestAnEventAckForMessagesNeverSentOrCountedTwiceIsIgnoredAndOpensNoSlot (a window of 2, RuntimeLoad.EventWindow).
func TestAnAckOfTheGoSideForMessagesNeverWrittenOrCountedTwiceIsIgnoredAndOpensNoSlot(t *testing.T) {
	open := func(t *testing.T) (*ctRig, *NodeReference, *ctSeen) {
		rig := ctStart(t, new(2))
		ref := rig.court(t)
		seen, _ := ctListen(t, ref, background)
		rig.holdAcks()
		return rig, ref, seen
	}
	t.Run("an ack for five messages when none was written: the window stays 2", func(t *testing.T) {
		rig, ref, seen := open(t)
		rig.ack(t, 5)
		ctCall(t, ref, "burst", 4)
		if got, want := seen.got(), ctRange(1, 2); !reflect.DeepEqual(got, want) {
			t.Fatalf("delivered %v, want %v", got, want)
		}
	})
	t.Run("an ack counting more than is outstanding frees only what is outstanding", func(t *testing.T) {
		rig, ref, seen := open(t)
		ctCall(t, ref, "burst", 1)
		rig.ack(t, 3)
		ctCall(t, ref, "burst", 4)
		if got, want := seen.got(), ctRange(1, 3); !reflect.DeepEqual(got, want) {
			t.Fatalf("delivered %v, want %v", got, want)
		}
	})
	t.Run("the same message acknowledged twice frees one slot", func(t *testing.T) {
		rig, ref, seen := open(t)
		ctCall(t, ref, "burst", 1)
		rig.ack(t, 1)
		rig.ack(t, 1)
		ctCall(t, ref, "burst", 4)
		if got, want := seen.got(), ctRange(1, 3); !reflect.DeepEqual(got, want) {
			t.Fatalf("delivered %v, want %v", got, want)
		}
	})
}

// A Go listener that stops mid stream, by Stop or by its context's abort, gets nothing more; another one goes on; and the
// object's events are still sent and acknowledged: forty more, one at a time, never fill the window of 32.
func TestAGoListenerThatStopsMidStreamGetsNothingMoreAndTheEventsAreStillSentAndAcknowledged(t *testing.T) {
	rig := ctStart(t, nil)
	ref := rig.court(t)
	stopped, sub := ctListen(t, ref, background)
	ctx, cancel := context.WithCancel(background)
	defer cancel()
	aborted, _ := ctListen(t, ref, ctx)
	kept, _ := ctListen(t, ref, background)
	for y := 1; y <= 2; y++ {
		ctCall(t, ref, "serve", float64(y))
		rig.acknowledged(t)
	}
	sub.Stop()
	sub.Stop()
	cancel()
	for y := 3; y <= 42; y++ {
		ctCall(t, ref, "serve", float64(y))
		rig.acknowledged(t)
	}
	if got, want := stopped.got(), ctRange(1, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("the stopped listener got %v, want %v", got, want)
	}
	if got, want := aborted.got(), ctRange(1, 2); !reflect.DeepEqual(got, want) {
		t.Fatalf("the listener whose context was aborted got %v, want %v", got, want)
	}
	if got, want := kept.got(), ctRange(1, 42); !reflect.DeepEqual(got, want) {
		t.Fatalf("the listener that stayed got %v, want %v", got, want)
	}
	rig.counted(t, map[string]float64{"chord.events.sent": 42, "chord.events.dropped.window": 0})
}

// The Node host's reference is tapped when it binds for the Go side, not at a listener (decision "subscribe at bind": a
// link sends a hold's events whether its consumer listens or not, and its consumer acknowledges every message, Pi
// remote/consumer.ts:312-320): forty events with no Go listener are sent and acknowledged, none dropped, and a listener
// added then gets the next event and none of the earlier ones.
func TestEventsOfANodeObjectAreSentAndAcknowledgedWhileNoGoListenerIsOn(t *testing.T) {
	rig := ctStart(t, nil)
	ref := rig.court(t)
	for y := 1; y <= 40; y++ {
		ctCall(t, ref, "serve", float64(y))
		rig.acknowledged(t)
	}
	seen, _ := ctListen(t, ref, background)
	ctCall(t, ref, "serve", 41)
	if got, want := seen.got(), []float64{41}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the listener got %v, want %v: nothing emitted before it was on", got, want)
	}
	rig.counted(t, map[string]float64{"chord.events.sent": 41, "chord.events.dropped.window": 0})
}

// A released reference's listeners get nothing more, its On fails `released`, and the Node host untaps its own reference
// before it releases it: of two references to one object, each tapped under its own handle, the one that stays is the
// only one an event is sent for.
func TestAReleasedNodeReferenceIsUntappedAndItsListenersEnd(t *testing.T) {
	rig := ctStart(t, nil)
	first, second := rig.court(t), rig.court(t)
	gone, _ := ctListen(t, first, background)
	kept, _ := ctListen(t, second, background)
	ctCall(t, second, "serve", 1)
	rig.counted(t, map[string]float64{"chord.events.sent": 2})
	first.Release()
	if _, err := first.Events("paddle").On(func(contracts.EventDelivery[delta.JsonValue]) {}, background); err == nil || err.Error() != releasedError("test.court").Error() {
		t.Fatalf("On of a released reference = %v, want %v", err, releasedError("test.court"))
	}
	ctCall(t, second, "serve", 2)
	rig.counted(t, map[string]float64{"chord.events.sent": 3})
	first.mu.Lock()
	left := len(first.Events("paddle").subscriptions)
	first.mu.Unlock()
	if left != 0 {
		t.Fatalf("the released reference keeps %d subscriptions, want none", left)
	}
	if got, want := gone.got(), []float64{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the released reference's listener got %v, want %v", got, want)
	}
	if got, want := kept.got(), []float64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("the other reference's listener got %v, want %v", got, want)
	}
}

// The `at` a Go listener gets is the Node host's stamp of the emit, as over a link (value and stamp unchanged: Pi
// remote/remote-object.ts:547), never the time the event was read.
func TestTheGoListenerGetsTheNodeHostsStampOfTheEmit(t *testing.T) {
	rig := ctStart(t, nil)
	ref := rig.court(t)
	seen, _ := ctListen(t, ref, background)
	ctCall(t, ref, "stamped", 7)
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if !reflect.DeepEqual(seen.ys, []float64{7}) || seen.ats[0] != 1700000000123 {
		t.Fatalf("the listener got ys %v at %v, want 7 at 1700000000123", seen.ys, seen.ats)
	}
}

// A listener on the Node host that emits while a flush calls it queues the next flush: its event crosses in a message of
// its own, after the first, and takes a slot of its own, as on a link (Pi host/events.ts:104-106). With 31 messages
// unacknowledged the flush of the rally's first sample is the 32nd message and is written; the flush of the sample its
// listener answers with finds the window full and is dropped. (A bridge that subscribed with `reference[name].on` and
// closed a batch in a microtask would send both in one message, the 32nd.)
func TestAnEventANodeListenerEmitsInAFlushCrossesInTheNextMessageAndTakesItsOwnSlot(t *testing.T) {
	rig := ctStart(t, nil)
	ref := rig.court(t)
	seen, _ := ctListen(t, ref, background)
	rig.holdAcks()
	ctCall(t, ref, "burst", 31)
	ctCall(t, ref, "rally", 100)
	rig.counted(t, map[string]float64{"chord.events.sent": 32, "chord.events.dropped.window": 1})
	if got, want := seen.got(), append(ctRange(1, 31), 100); !reflect.DeepEqual(got, want) {
		t.Fatalf("delivered %v, want %v: the sample the listener emitted is the 33rd message", got, want)
	}
}

// A process that ends mid stream leaves nothing behind on the Go side: the call in flight fails with the exit, the sender
// was joined when Exited closed, the reference's subscriptions are let go, a new On fails with the exit, no listener is
// called again, and no goroutine started since before the host was loaded is left.
func TestANodeHostThatEndsMidStreamLeavesNoSubscriptionAndNoGoroutine(t *testing.T) {
	for _, mode := range []string{"close", "kill"} {
		t.Run(mode, func(t *testing.T) {
			baseline := testenv.BaselineGoroutines()
			rig := ctOpen(t, nil)
			ref, err := rig.node.Get(background, ctCourt, delta.JsonObjectOf("id", "c1"), host.GetOptions{})
			if err != nil {
				_ = rig.node.Close(background)
				t.Fatal(err)
			}
			third := make(chan struct{})
			var once sync.Once
			seen := &ctSeen{}
			if _, err := ref.Events("paddle").On(func(delivery contracts.EventDelivery[delta.JsonValue]) {
				seen.listener(delivery)
				if len(seen.got()) == 3 {
					once.Do(func() { close(third) })
				}
			}, background); err != nil {
				t.Fatal(err)
			}
			failed := make(chan error, 1)
			go func() {
				_, err := ref.Call("burst", 1_000_000, background)
				failed <- err
			}()
			select {
			case <-third:
			case err := <-failed:
				t.Fatalf("burst ended before three events arrived: %v", err)
			case <-time.After(30 * time.Second):
				t.Fatal("three events did not arrive")
			}
			if mode == "kill" {
				rig.node.kill()
			} else {
				_ = rig.node.Close(background)
			}
			<-rig.node.Exited()
			if err := <-failed; err == nil {
				t.Fatal("the call in flight returned a result after the process ended")
			}
			if stacks := senderStacks(); stacks != "" {
				t.Fatalf("a goroutine of the sender outlived the process:\n%s", stacks)
			}
			if _, err := ref.Events("paddle").On(func(contracts.EventDelivery[delta.JsonValue]) {}, background); err == nil || err.Error() != rig.node.exitError().Error() {
				t.Fatalf("On after the process ended = %v, want %v", err, rig.node.exitError())
			}
			eventually(t, 15*time.Second, "the reference's subscriptions to be let go", func() bool {
				ref.mu.Lock()
				defer ref.mu.Unlock()
				return ref.eventsEnded && len(ref.Events("paddle").subscriptions) == 0
			})
			delivered := len(seen.got())
			ref.Release()
			_ = rig.node.Close(background)
			baseline.RequireEnded(t, 10*time.Second)
			if got := len(seen.got()); got != delivered {
				t.Fatalf("the listener was called %d times after the process ended", got-delivered)
			}
		})
	}
}
