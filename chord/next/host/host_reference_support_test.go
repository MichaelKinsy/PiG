package host

import (
	"runtime"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/loopseam"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/internal/storeseam"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Test support of the reference-side files (reference, presence, route, member-source, keyed-members, follow): what the
// upstream tests build with `framesOf`, `delivered()` and small local helpers, over the support of host_next_support_test.go.

// deliveredOn is support.ts:138 `delivered()` (`new Promise((resolve) => setTimeout(resolve, 0))`) for the hosts. Its contract
// is what that `setTimeout(0)` waits for in Pi: every queued watch frame delivered, and every microtask chain queued before it
// run to its next real wait; not what a Pi test would still be waiting for after it (a plugin's gate, a timer, a link frame not
// yet received, a commit held by the test). Go: (1) the hosts' delivery schedulers are idle; (2) the host's loop has no joined
// hop in flight (loopseam.Quiesce: its count of the goroutines that run a microtask queued from outside a section, which
// signals when it falls to zero: no polling, no sleep, no deadline); (3) the store's reads index has no reload task running
// (storeseam.ReadTasks: the unawaited `void open.load()` of a changed binding, owned by the index); repeated until all hold,
// since a delivery's listener queues microtasks, a hop advances frames and a reload's adoption queues a delivery.
func deliveredOn(hosts ...Host) {
	for _, host := range hosts {
		h := host.(*chordHost)
		for {
			if h.store != nil {
				<-storeseam.ReadTasks(h.store)
			}
			h.scheduler.Wait()
			quiesce(h.loop())
			if !h.scheduler.Busy() && storeIdle(h) {
				break
			}
		}
	}
}

// quiesce is loopseam.Quiesce for deliveredOn. A join that stalled (a task blocked where the loop cannot see it) ends the binary at
// once with the join's report: the report is a panic, and a panic in the test's own goroutine is printed only after the test's
// deferred calls returned, which a test that holds a gate on purpose (a held commit, a native that stays opening) never lets them
// do: closeHosts waits for what the gate holds. So the panic is raised from a goroutine no deferred call waits for, as the handler
// stall reporter's is (TestMain).
func quiesce(loop any) {
	defer func() {
		if report := recover(); report != nil {
			go func() { panic(report) }()
			select {}
		}
	}()
	loopseam.Quiesce(loop)
}

// storeIdle is whether the reloads a changed binding started in the host's store (its reads index owns them: Pi's unawaited
// `void open.load()`, whose chain `delivered()` runs to its end) have ended; the barrier is closed then, with no polling.
func storeIdle(h *chordHost) bool {
	if h.store == nil {
		return true
	}
	select {
	case <-storeseam.ReadTasks(h.store):
		return true
	default:
		return false
	}
}

// opsOf is the ops of every frame as JSON arrays: `frames.map(([, ops]) => ops)`.
func opsOf(frames []nexttest.Frame[delta.JsonValue]) []any {
	out := make([]any, len(frames))
	for i, frame := range frames {
		ops := make([]any, len(frame.Ops))
		for j, op := range frame.Ops {
			ops[j] = []any(op)
		}
		out[i] = ops
	}
	return out
}

// valuesOfFrames is the value of every frame: `frames.map(([value]) => value)`.
func valuesOfFrames(frames []nexttest.Frame[delta.JsonValue]) []any {
	out := make([]any, len(frames))
	for i, frame := range frames {
		out[i] = frame.Value
	}
	return out
}

// countOf is the number `host.activity.value[name]`.
func countOf(t testing.TB, host Host, name string) float64 {
	t.Helper()
	var value delta.JsonValue
	var err error
	host.(*chordHost).loop().Run(func() { value, err = host.Activity().Value() })
	must(t, err)
	number, _ := value.(*delta.JsonObject).Value(name).(float64)
	if float, ok := value.(*delta.JsonObject).Value(name).(int); ok {
		number = float64(float)
	}
	return number
}

// closingOnSend is `link`, closing itself on a send once drop is armed, as a transport whose send fails.
func closingOnSend(end link.Link, drop *atomic.Bool) link.Link {
	return &closingLink{Link: end, drop: drop}
}

type closingLink struct {
	link.Link
	drop *atomic.Bool
}

func (c *closingLink) Send(message []byte) {
	if c.drop.Load() {
		c.Close()
		return
	}
	c.Link.Send(message)
}

// takeReceived empties what the raw peer received: `peer.received.length = 0`.
func (p *RawPeer) takeReceived() []link.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	received := p.received
	p.received = nil
	return received
}

// serverHolds is every hold the host's links serve, in order: Pi's `instancesOf(Hold, "start")` reaches them through the
// prototype; a Go test reaches them through the links' provider sides.
func serverHolds(host Host) []*hold {
	var holds []*hold
	h := host.(*chordHost)
	h.loop().Run(func() {
		for _, each := range h.links {
			for _, provider := range each.providers.values() {
				for _, entry := range provider.entries.values() {
					if held, ok := entry.(*hold); ok {
						holds = append(holds, held)
					}
				}
			}
		}
	})
	return holds
}

// holdOf is a `hold` message for number naming typ and the test server's address: params is the JSON text of the params.
func holdOf(typ *contracts.Contract, number int, params string) *link.HoldMessage {
	return &link.HoldMessage{
		Hold: number, Type: typ.Name, Version: typ.Version, Fingerprint: typ.Fingerprint,
		Params: link.Text(params), Address: serverWireAddress,
	}
}

// expectCollectedEventually is nexttest.ExpectCollected for referents whose last holders go away on tasks of the host (a
// closing host drains its tasks, a released hold ends in the background): the collection is polled until nothing holds them.
func expectCollectedEventually[T any](t testing.TB, refs ...weak.Pointer[T]) {
	t.Helper()
	scopeEventually(t, "every referent is collected", func() bool {
		runtime.GC()
		for _, ref := range refs {
			if ref.Value() != nil {
				return false
			}
		}
		return true
	})
}

// failLiveThen is `failLive(self, error); then()` as one synchronous block of Pi's event loop: nothing of the host runs
// between the two (a microtask such as the failed object's stop of following cannot land between them), so they are one
// section of the host's loop.
func failLiveThen(host Host, self Self, failure *data.ChordError, then func()) {
	host.(*chordHost).loop().Run(func() {
		failLive(self, failure)
		then()
	})
}

// awaitWatchCalls waits until calls has n items. The Go form of Pi's `await delivered()` after a failure: the failed object's stop
// of following is a task of the loop that settles after the test's goroutine goes on (Pi's microtask lands first).
func awaitWatchCalls[T any](t testing.TB, what string, calls *nexttest.Collector[T], n int) {
	t.Helper()
	scopeEventually(t, what, func() bool { return len(calls.Items()) >= n })
}

// takenOf is how many messages of kind the raw peer received and did not take, taken out: Pi's `taken(peer, kind)`.
func takenOf(peer *RawPeer, kind string) int {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	kept := make([]link.Message, 0, len(peer.received))
	count := 0
	for _, message := range peer.received {
		if message.Kind() == kind {
			count++
		} else {
			kept = append(kept, message)
		}
	}
	peer.received = kept
	return count
}

// holdsAt is how many holds peer got since the last count (taken out), once the host handled what the peer sent before:
// Pi's `holdsAt(peer)` of host.test.ts.
func holdsAt(t testing.TB, peer *RawPeer) int {
	t.Helper()
	rhSync(t, peer)
	return takenOf(peer, "hold")
}

// remoteBindings is how many bindings the remote objects on host's parent path count, and how many entries its sharing
// table has: a Go test waits on the first where Pi's turn order has every getter bound already.
func remoteBindings(host Host) (bindings, entries int) {
	h := host.(*chordHost)
	h.loop().Run(func() {
		for _, holder := range h.parent.holders() {
			if remote, is := holder.(*remoteObject); is {
				bindings += len(remote.bindings)
			}
		}
		entries = len(h.parent.shared)
	})
	return bindings, entries
}
