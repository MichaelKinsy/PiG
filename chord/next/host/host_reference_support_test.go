package host

import (
	"runtime"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// Test support of the reference-side files (reference, presence, route, member-source, keyed-members, follow): what the
// upstream tests build with `framesOf`, `delivered()` and small local helpers, over the support of host_next_support_test.go.

// deliveredOn is support.ts `delivered()` for the hosts: every queued watch frame of their batches has been delivered
// (the hosts' delivery schedulers are idle).
func deliveredOn(hosts ...Host) {
	for _, host := range hosts {
		host.(*chordHost).scheduler.Wait()
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
