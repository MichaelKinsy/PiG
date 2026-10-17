package host

// Ports packages/chord/test/flow-units.test.ts (Pi b45e0ebc): flow control with units. First units pass the window, so a
// get never waits behind other streams; waiting ops are capped per link; relays ask downstream for a reset when they
// drop ops.

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var unitsBlob = Remote("t.e8-blob", RemoteDef{
	Version: 1, Input: tObject("name", tString()), Host: NewHostNamed("server"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"lines", tArray(tString())}},
		Methods: Fields[MethodSpec]{{"push", Method(tString(), nil)}, {"ping", Method(nil, tString())}},
	}}},
})

// unitsLines is about kib KiB of lines.
func unitsLines(kib int, tag string) []any {
	out := make([]any, kib)
	for index := range out {
		out[index] = fmt.Sprintf("%s%d:%s", tag, index, strings.Repeat("x", 1000))
	}
	return out
}

func serveUnitsBlob(t *testing.T, host *HostImpl, size func(name string) int) {
	t.Helper()
	requestsServe(t, host.Serve, unitsBlob, func(input any, _ Principal, self Self) (Implementation, error) {
		name := inputField(input, "name").(string)
		state, err := self.State("lines", tArray(tString()), unitsLines(size(name), name), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main", "lines", state,
			"push", methodFunc(func(line any, ctx Ctx) (any, error) {
				return nil, state.Change(ctx, func(draft any) {
					if _, err := draft.(*delta.Array).Push(line); err != nil {
						panic(err)
					}
				})
			}),
			"ping", methodFunc(func(any, Ctx) (any, error) { return "pong", nil }),
		), nil
	}, ServeOptions{})
}

type unitsClient struct {
	host *HostImpl
	link *countingLink
}

func unitsClientOf(t *testing.T, server *HostImpl, window int, pendingCap *int) unitsClient {
	t.Helper()
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}, LinkOptions: LinkOptions{Window: &window, PendingCap: pendingCap}}); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up)
	return unitsClient{host: requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}}), link: link}
}

// downstream is the server's link to the client (the one accepted as a downstream link).
func downstream(server *HostImpl) *Connection {
	var found *Connection
	server.run(func() {
		for _, connection := range server.connections.Keys() {
			if connection.role == "downstream" {
				found = connection
				return
			}
		}
	})
	return found
}

func unitsHold(t *testing.T, host *HostImpl, name string) Ref {
	t.Helper()
	ref, err := host.Hold(unitsBlob, map[string]any{"name": name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func unitsLen(t *testing.T, ref Ref) int {
	t.Helper()
	return len(requestsReadable(t, ref, "lines").Value().([]any))
}

// flow-units.test.ts:81-103 "with large streams waiting behind a full window, a new get resolves at once and calls are
// answered"
func TestFlowUnitsWithLargeStreamsWaitingBehindAFullWindowANewGetResolvesAtOnceAndCallsAreAnswered(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveUnitsBlob(t, server, func(name string) int {
		if name == "small" {
			return 1
		}
		return 500
	})
	client := unitsClientOf(t, server, 64*1024, nil)
	var bigs []Ref
	for _, name := range []string{"a", "b", "c"} {
		big := unitsHold(t, client.host, name)
		if err := readyErr(t, big); err != nil {
			t.Fatal(err)
		}
		bigs = append(bigs, big)
	}
	client.link.pause()
	// Changes to every large stream now wait for acknowledgements that do not come.
	for _, big := range bigs {
		for i := range 50 {
			mustCall(t, big, "push", fmt.Sprintf("%d:%s", i, strings.Repeat("y", 2000)), nil)
		}
	}
	small := unitsHold(t, client.host, "small")
	if err := readyErr(t, small); err != nil {
		t.Fatal(err)
	}
	if got := unitsLen(t, small); got != 1 {
		t.Fatalf("small has %d lines, want 1", got)
	}
	if got := mustCall(t, bigs[0], "ping", nil, nil); got != "pong" {
		t.Fatalf("ping = %v", got)
	}
	if got := unitsLen(t, bigs[0]); got >= 550 {
		t.Fatalf("the large streams still wait, but have %d lines", got)
	}
	client.link.release()
	waitFor(t, "every large stream complete", func() bool {
		for _, big := range bigs {
			if unitsLen(t, big) != 550 {
				return false
			}
		}
		return true
	})
}

// flow-units.test.ts:105-118 "a snapshot larger than the window arrives whole; later changes wait for acknowledgements"
func TestFlowUnitsASnapshotLargerThanTheWindowArrivesWholeLaterChangesWaitForAcknowledgements(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveUnitsBlob(t, server, func(string) int { return 300 })
	client := unitsClientOf(t, server, 16*1024, nil)
	client.link.pause()
	big := unitsHold(t, client.host, "chat")
	if err := readyErr(t, big); err != nil {
		t.Fatal(err)
	}
	if got := unitsLen(t, big); got != 300 {
		t.Fatalf("the snapshot has %d lines, want 300", got)
	}
	for i := range 40 {
		mustCall(t, big, "push", fmt.Sprintf("%d:%s", i, strings.Repeat("z", 1000)), nil)
	}
	if got := unitsLen(t, big); got >= 340 {
		t.Fatalf("later changes should wait, %d lines", got)
	}
	client.link.release()
	waitFor(t, "340 lines", func() bool { return unitsLen(t, big) == 340 })
}

// unsizedLink is a link that reports no sizes, as Node's IPC channel (flow-units.test.ts:120-131).
type unsizedLink struct{ Link }

func (link unsizedLink) Send(frame Frame) (int, error) {
	_, err := link.Link.Send(frame)
	return 0, err
}

// flow-units.test.ts:120-149 "a link that reports no sizes (IPC) never holds state back"
func TestFlowUnitsALinkThatReportsNoSizesNeverHoldsStateBack(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveUnitsBlob(t, server, func(string) int { return 1 })
	down, up := MemoryLinkPair()
	window := 1
	if err := server.Accept(unsizedLink{down}, AcceptOptions{Principal: map[string]any{"user": "u"}, LinkOptions: LinkOptions{Window: &window}}); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up)
	host := requestsHost(t, HostOptions{Name: "worker", Kind: KindClient, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	link.pause()
	blob := unitsHold(t, host, "x")
	if err := readyErr(t, blob); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		mustCall(t, blob, "push", fmt.Sprint(i), nil)
	}
	waitFor(t, "21 lines", func() bool { return unitsLen(t, blob) == 21 })
}

// flow-units.test.ts:151-178 "waiting ops stay under the per-link cap however many streams stall; each then gets one
// reset"
func TestFlowUnitsWaitingOpsStayUnderThePerLinkCapHoweverManyStreamsStallEachThenGetsOneReset(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveUnitsBlob(t, server, func(string) int { return 1 })
	capBytes := 8 * 1024
	client := unitsClientOf(t, server, 1024, &capBytes)
	const streams = 6
	var blobs []Ref
	for i := range streams {
		blob := unitsHold(t, client.host, fmt.Sprintf("s%d", i))
		if err := readyErr(t, blob); err != nil {
			t.Fatal(err)
		}
		blobs = append(blobs, blob)
	}
	connection := downstream(server)
	client.link.pause()
	peak := 0
	for round := range 30 {
		for _, blob := range blobs {
			mustCall(t, blob, "push", fmt.Sprintf("%d:%s", round, strings.Repeat("w", 200)), nil)
		}
		var bytes int
		server.run(func() { _, bytes = connection.waiting() })
		peak = max(peak, bytes)
	}
	if peak > capBytes {
		t.Fatalf("waiting ops %d exceed the cap %d", peak, capBytes)
	}
	before := len(client.link.receivedFrames())
	client.link.release()
	waitFor(t, "every stream complete", func() bool {
		for _, blob := range blobs {
			if unitsLen(t, blob) != 31 {
				return false
			}
		}
		return true
	})
	per := map[int]int{}
	resets := 0
	for _, frame := range stateFrames(client.link.receivedFrames()[before:]) {
		if frame.As == "reset" {
			resets++
			per[frame.Hold]++
		}
	}
	if resets < 1 {
		t.Fatal("dropped streams came back as resets")
	}
	for hold, count := range per {
		if count > 2 {
			t.Fatalf("hold %d got %d resets, want about one per stream", hold, count)
		}
	}
}

var unitsRelayed = Remote("t.e8-relayed", RemoteDef{
	Version: 1, Input: tObject("session", tString()), Host: NewHostFamily("session:", "session"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"n", tNumber()}},
		Methods: Fields[MethodSpec]{{"add", Method(tNumber(), nil)}},
	}}},
})

type unitsCluster struct {
	server   *HostImpl
	mu       sync.Mutex
	worker   *HostImpl
	toWorker *countingLink
}

func newUnitsCluster(t *testing.T) *unitsCluster {
	t.Helper()
	c := &unitsCluster{server: requestsHost(t, HostOptions{Name: "server"})}
	c.server.Route("session:", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.toWorker != nil {
				return c.toWorker, nil
			}
			down, up := MemoryLinkPair()
			worker := requestsHost(t, HostOptions{Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
			requestsServe(t, worker.Serve, unitsRelayed, func(_ any, _ Principal, self Self) (Implementation, error) {
				n, err := self.State("n", tNumber(), float64(0), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("variant", "main", "n", n, "add", methodFunc(func(amount any, ctx Ctx) (any, error) {
					return nil, n.ChangeValue(ctx, func(value any) any { return value.(float64) + amount.(float64) })
				})), nil
			}, ServeOptions{})
			c.worker, c.toWorker = worker, newCountingLink(down)
			return c.toWorker, nil
		},
	})
	return c
}

func (c *unitsCluster) browser(t *testing.T, principal any, window int) unitsClient {
	t.Helper()
	down, up := MemoryLinkPair()
	options := AcceptOptions{Principal: principal}
	if window > 0 {
		pendingCap := window * 2
		options.LinkOptions = LinkOptions{Window: &window, PendingCap: &pendingCap}
	}
	if err := c.server.Accept(down, options); err != nil {
		t.Fatal(err)
	}
	link := newCountingLink(up)
	return unitsClient{host: requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: link, ConnectOptions: ConnectOptions{Principal: kindServer()}}}), link: link}
}

// served is the holds the worker serves for its parent; relayed the holds the server relays to its children.
func (c *unitsCluster) served() int {
	c.mu.Lock()
	worker := c.worker
	c.mu.Unlock()
	if worker == nil {
		return 0
	}
	n := 0
	worker.run(func() {
		if worker.upstream != nil {
			n = worker.upstream.served.Len()
		}
	})
	return n
}

func (c *unitsCluster) relayed() int {
	n := 0
	c.server.run(func() {
		for _, connection := range c.server.connections.Keys() {
			if connection.peer.kind == KindHost {
				n += connection.relayedRefs.Len()
			}
		}
	})
	return n
}

func unitsCounter(t *testing.T, host *HostImpl) Ref {
	t.Helper()
	ref, err := host.Hold(unitsRelayed, map[string]any{"session": "S1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// flow-units.test.ts:215-239 "each origin hold is one downstream hold; nothing is left after release, close or revoke"
func TestFlowUnitsRelayEachOriginHoldIsOneDownstreamHoldNothingIsLeftAfterReleaseCloseOrRevoke(t *testing.T) {
	c := newUnitsCluster(t)
	// Two tabs of one guest (two connections, one principal) and another guest.
	one := c.browser(t, map[string]any{"guest": "g"}, 0)
	two := c.browser(t, map[string]any{"guest": "g"}, 0)
	three := c.browser(t, map[string]any{"guest": "h"}, 0)
	a, a2 := unitsCounter(t, one.host), unitsCounter(t, one.host)
	b, d := unitsCounter(t, two.host), unitsCounter(t, three.host)
	for _, ref := range []Ref{a, a2, b, d} {
		if err := readyErr(t, ref); err != nil {
			t.Fatal(err)
		}
	}
	if got := c.toWorker.countSent("hold"); got != 3 {
		t.Fatalf("holds = %d, want 3: two tabs and another principal", got)
	}
	if got := c.served(); got != 3 {
		t.Fatalf("served = %d, want 3", got)
	}
	a.Release()
	a2.Release()
	waitFor(t, "two holds left", func() bool { return c.served() == 2 && c.relayed() == 2 })
	two.link.Close()
	waitFor(t, "one hold left", func() bool { return c.served() == 1 && c.relayed() == 1 })
	if err := c.server.InvalidatePrincipal(PrincipalMatch{Pattern: chordjson.ObjectOf("guest", "h")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "no hold left", func() bool { return c.served() == 0 && c.relayed() == 0 })
}

// flow-units.test.ts:241-270 "a stalled tab overflows and gets a reset while another tab keeps getting every change"
func TestFlowUnitsRelayAStalledTabOverflowsAndGetsAResetWhileAnotherTabKeepsGettingEveryChange(t *testing.T) {
	c := newUnitsCluster(t)
	slow := c.browser(t, map[string]any{"guest": "g"}, 256)
	fast := c.browser(t, map[string]any{"guest": "g"}, 0)
	stalled, live := unitsCounter(t, slow.host), unitsCounter(t, fast.host)
	for _, ref := range []Ref{stalled, live} {
		if err := readyErr(t, ref); err != nil {
			t.Fatal(err)
		}
	}
	slow.link.pause()
	var mu sync.Mutex
	var seen []float64
	requestsReadable(t, live, "n").Subscribe(func(value any) {
		mu.Lock()
		seen = append(seen, value.(float64))
		mu.Unlock()
	})
	for range 200 {
		mustCall(t, live, "add", float64(1), nil)
	}
	waitFor(t, "n = 200", func() bool { return requestsReadable(t, live, "n").Value() == float64(200) })
	mu.Lock()
	count := len(seen)
	mu.Unlock()
	if count <= 100 {
		t.Fatalf("the fast tab saw %d changes, want it to see them as they came", count)
	}
	if got := requestsReadable(t, stalled, "n").Value().(float64); got >= 200 {
		t.Fatalf("the stalled tab is at %v, it should wait", got)
	}
	if c.toWorker.countSent("resync") < 1 {
		t.Fatal("the relay should ask the worker for a reset")
	}
	slow.link.release()
	waitFor(t, "the stalled tab catches up", func() bool { return requestsReadable(t, stalled, "n").Value() == float64(200) })
}
