package host

// Ports packages/chord/test/flow.test.ts: link flow control. State waits while the peer's acknowledgements lag a
// window behind, coalesced per subscription; everything else is sent at once. Pi's awaits are blocking calls off the
// host loop.

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// ackGate is a client's end of a link that can hold back its acknowledgements, and records what it receives
// (flow.test.ts:25-61).
type ackGate struct {
	inner    Link
	mu       sync.Mutex
	received []Frame
	paused   bool
	held     Frame
}

func (gate *ackGate) Closed() bool { return gate.inner.Closed() }

func (gate *ackGate) Send(frame Frame) (int, error) {
	gate.mu.Lock()
	if _, isAck := frame.(AckFrame); isAck && gate.paused {
		gate.held = frame
		gate.mu.Unlock()
		return 0, nil
	}
	gate.mu.Unlock()
	return gate.inner.Send(frame)
}

func (gate *ackGate) pause() {
	gate.mu.Lock()
	gate.paused = true
	gate.mu.Unlock()
}

func (gate *ackGate) release() {
	gate.mu.Lock()
	gate.paused = false
	held := gate.held
	gate.held = nil
	gate.mu.Unlock()
	if held != nil {
		_, _ = gate.inner.Send(held)
	}
}

func (gate *ackGate) Receive(handler func(frame Frame)) {
	gate.inner.Receive(func(frame Frame) {
		gate.mu.Lock()
		gate.received = append(gate.received, frame)
		gate.mu.Unlock()
		handler(frame)
	})
}

func (gate *ackGate) OnClose(handler func(reason string)) { gate.inner.OnClose(handler) }
func (gate *ackGate) Close(reason ...string)              { gate.inner.Close(reason...) }

func (gate *ackGate) frames() []Frame {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return slices.Clone(gate.received)
}

// recordingLink is a link that records what it sends (flow.test.ts:191-206).
type recordingLink struct {
	inner Link
	mu    sync.Mutex
	sent  []Frame
}

func (link *recordingLink) Closed() bool { return link.inner.Closed() }
func (link *recordingLink) Send(frame Frame) (int, error) {
	link.mu.Lock()
	link.sent = append(link.sent, frame)
	link.mu.Unlock()
	return link.inner.Send(frame)
}
func (link *recordingLink) Receive(handler func(frame Frame))   { link.inner.Receive(handler) }
func (link *recordingLink) OnClose(handler func(reason string)) { link.inner.OnClose(handler) }
func (link *recordingLink) Close(reason ...string)              { link.inner.Close(reason...) }

func (link *recordingLink) count(t string) int {
	link.mu.Lock()
	defer link.mu.Unlock()
	n := 0
	for _, frame := range link.sent {
		if frame.T() == t {
			n++
		}
	}
	return n
}

// flowCounterOn is a counter on the server, or (t.flow-worker-counter) on the worker host w (flow.test.ts counterOn).
func flowCounterOn(name, host string) *Contract {
	return Remote(name, RemoteDef{
		Version: 1,
		Input:   tObject(),
		Host:    NewHostNamed(host),
		Variants: Fields[VariantDef]{{"main", VariantDef{
			State:   Fields[Schema]{{"count", linkCountSchema}},
			Methods: Fields[MethodSpec]{{"add", Method(tNumber(), tNumber())}, {"wait", Method(nil, nil)}},
		}}},
	})
}

var (
	flowCounter       = flowCounterOn("t.flow-counter", "server")
	flowWorkerCounter = flowCounterOn("t.flow-worker-counter", "w")
)

// serveFlowCounter serves typ; its wait method returns once waiting is opened (nil: at once).
func serveFlowCounter(t *testing.T, host *HostImpl, typ *Contract, waiting *requestsLatch) {
	t.Helper()
	err := host.Serve(typ, func(_ any, _ Principal, self Self) (Implementation, error) {
		count, err := self.State("count", linkCountSchema, delta.JsonObjectOf("n", float64(0)), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main",
			"count", count,
			"add", methodFunc(func(args any, ctx Ctx) (any, error) {
				if err := count.Change(ctx, func(draft any) {
					object := draft.(*delta.Object)
					_ = object.Set("n", object.Get("n").(float64)+args.(float64))
				}); err != nil {
					return nil, err
				}
				return countN(count.Value()), nil
			}),
			"wait", methodFunc(func(any, Ctx) (any, error) {
				if waiting != nil {
					waiting.wait()
				}
				return nil, nil
			}),
		), nil
	}, ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// flowClient is a client of server (accepted with window) whose acknowledgements can be held back.
func flowClient(t *testing.T, server *HostImpl, window int) (*HostImpl, *ackGate) {
	t.Helper()
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}, LinkOptions: LinkOptions{Window: &window}}); err != nil {
		t.Fatal(err)
	}
	gate := &ackGate{inner: up}
	return requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: gate, ConnectOptions: ConnectOptions{Principal: kindServer()}}}), gate
}

func stateFrames(frames []Frame) []StateFrame {
	var out []StateFrame
	for _, frame := range frames {
		if state, ok := frame.(StateFrame); ok {
			out = append(out, state)
		}
	}
	return out
}

// countValues records the values a count member shows.
type countValues struct {
	mu     sync.Mutex
	values []float64
}

func (values *countValues) add(value any) {
	values.mu.Lock()
	values.values = append(values.values, countN(value))
	values.mu.Unlock()
}

// last is the latest value's n; -1 before any.
func (values *countValues) last() float64 {
	values.mu.Lock()
	defer values.mu.Unlock()
	if len(values.values) == 0 {
		return -1
	}
	return values.values[len(values.values)-1]
}

func flowGet(t *testing.T, host *HostImpl, typ *Contract) Ref {
	t.Helper()
	return requestsGet(t, host, typ, requestsEmpty(), nil)
}

func addOne(t *testing.T, counter Ref) float64 {
	t.Helper()
	value, err := callMember(counter, "add", float64(1), nil)
	if err != nil {
		t.Fatal(err)
	}
	return value.(float64)
}

// flow.test.ts:114-136
func TestLinkFlowControlRefsStateBackWhileTheWindowIsFullAnswersCallsAndSendsOneCoalescedFrame(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveFlowCounter(t, server, flowCounter, nil)
	host, gate := flowClient(t, server, 200)
	counter := flowGet(t, host, flowCounter)
	var values countValues
	requestsReadable(t, counter, "count").Subscribe(values.add)
	waitFor(t, "the first value", func() bool { return values.last() >= 0 })

	gate.pause()
	var result float64
	for range 40 {
		result = addOne(t, counter)
	}
	// Every call was answered, though most state is still waiting for an acknowledgement.
	if result != 40 {
		t.Fatalf("last result %v, want 40", result)
	}
	if values.last() >= 40 {
		t.Fatal("state waits behind the window")
	}
	before := len(stateFrames(gate.frames()))

	gate.release()
	waitFor(t, "n = 40", func() bool { return values.last() == 40 })
	if got := len(stateFrames(gate.frames())) - before; got != 1 {
		t.Fatalf("the waiting ops arrive as one frame, got %d", got)
	}
}

// flow.test.ts:138-160
func TestLinkFlowControlReplacesOpsThatOutgrowTheirSlotWithTheCurrentValue(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveFlowCounter(t, server, flowCounter, nil)
	host, gate := flowClient(t, server, 64)
	counter := flowGet(t, host, flowCounter)
	var values countValues
	requestsReadable(t, counter, "count").Subscribe(values.add)
	waitFor(t, "the first value", func() bool { return values.last() >= 0 })

	gate.pause()
	before := len(gate.frames())
	for range 100 {
		addOne(t, counter)
	}
	gate.release()
	waitFor(t, "n = 100", func() bool { return values.last() == 100 })
	sent := stateFrames(gate.frames()[before:])
	if !slices.ContainsFunc(sent, func(frame StateFrame) bool { return frame.As == "reset" }) {
		t.Fatal("a reset replaced the ops")
	}
	if len(sent) >= 10 {
		t.Fatalf("few state frames, not one per change (%d)", len(sent))
	}
}

// flow.test.ts:174-193 (b45e0ebc): releasing the references drops what waits for them.
func TestLinkFlowControlDropsWhatWaitsForAReferenceThatIsReleased(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveFlowCounter(t, server, flowCounter, nil)
	host, gate := flowClient(t, server, 64)
	counter := flowGet(t, host, flowCounter)
	if err := readyErr(t, counter); err != nil {
		t.Fatal(err)
	}
	other := flowGet(t, host, flowCounter)
	if err := readyErr(t, other); err != nil {
		t.Fatal(err)
	}

	gate.pause()
	for range 10 {
		addOne(t, counter)
	}
	counter.Release()
	other.Release()
	before := len(gate.frames())
	gate.release()
	time.Sleep(10 * time.Millisecond)
	if got := stateFrames(gate.frames()[before:]); len(got) != 0 {
		t.Fatalf("state frames after the references were released: %v", got)
	}
}

// flow.test.ts:195-240 (b45e0ebc)
func TestLinkFlowControlCoalescesRelayedStateAndAsksDownstreamForAResetWhenItOverflows(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	var toWorker *recordingLink
	var mu sync.Mutex
	server.Route("w", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			down, up := MemoryLinkPair()
			worker := requestsHost(t, HostOptions{Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
			serveFlowCounter(t, worker, flowWorkerCounter, nil)
			link := &recordingLink{inner: down}
			mu.Lock()
			toWorker = link
			mu.Unlock()
			return link, nil
		},
	})
	host, gate := flowClient(t, server, 64)
	counter := flowGet(t, host, flowWorkerCounter)
	var values countValues
	requestsReadable(t, counter, "count").Subscribe(values.add)
	waitFor(t, "the first value", func() bool { return values.last() >= 0 })
	mu.Lock()
	link := toWorker
	mu.Unlock()
	if got := link.count("resync"); got != 0 {
		t.Fatalf("resyncs = %d, want 0", got)
	}

	gate.pause()
	for range 100 {
		addOne(t, counter)
	}
	// An overflow drops the waiting ops and asks the worker for a reset (which then waits in the slot).
	if link.count("resync") < 1 {
		t.Fatal("the overflowing stream was resynced downstream")
	}
	if link.count("sub") != 0 || link.count("unsub") != 0 {
		t.Fatal("the relay sent sub or unsub downstream")
	}
	gate.release()
	waitFor(t, "n = 100", func() bool { return values.last() == 100 })
}

// flow.test.ts:224-241
func TestLinkFlowControlNeitherWindowsNorAcknowledgesAPeerWithoutAcks(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	serveFlowCounter(t, server, flowCounter, nil)
	down, raw := MemoryLinkPair()
	window := 1
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}, LinkOptions: LinkOptions{Window: &window}}); err != nil {
		t.Fatal(err)
	}
	frames := &frameLog{}
	raw.Receive(frames.add)
	_, _ = raw.Send(HelloFrame{Protocol: ProtocolVersion, Name: "raw", Kind: KindClient, CodeVersion: 0})
	_, _ = raw.Send(HoldFrame{ID: 1, At: "server", Token: flowCounter.Description, Input: map[string]any{}})
	for i := range 5 {
		_, _ = raw.Send(CallFrame{ID: 3 + i, Hold: 1, Member: "add", Args: float64(1), HasArgs: true})
	}
	waitFor(t, "five results", func() bool {
		n := 0
		for _, frame := range frames.list() {
			if _, ok := frame.(ResultFrame); ok {
				n++
			}
		}
		return n == 5
	})
	time.Sleep(5 * time.Millisecond)
	got := frames.list()
	if n := len(stateFrames(got)); n != 6 {
		t.Fatalf("the snapshot and one frame per change: %d state frames", n)
	}
	if slices.ContainsFunc(got, func(frame Frame) bool { return frame.T() == "ack" }) {
		t.Fatal("an ack was sent")
	}
	if hello, ok := got[0].(HelloFrame); !ok || !hello.Acks {
		t.Fatalf("first frame %#v, want a hello with acks", got[0])
	}
}

// flow.test.ts:243-252
func TestLinkFlowControlClosesTheLinkOnAnAcknowledgementOfFramesNeverSent(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	down, raw := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}}); err != nil {
		t.Fatal(err)
	}
	raw.Receive(func(Frame) {})
	_, _ = raw.Send(HelloFrame{Name: "raw", Kind: KindClient, CodeVersion: 0, Acks: true})
	_, _ = raw.Send(AckFrame{N: 99})
	waitFor(t, "the link closed", raw.Closed)
}

// flow.test.ts:256-276
func TestLinkCallLimitAnswersCallsBeyondMaxCallsWithTooManyCallsAndKeepsTheLink(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	waiting := newRequestsLatch()
	t.Cleanup(waiting.open)
	serveFlowCounter(t, server, flowCounter, waiting)
	down, up := MemoryLinkPair()
	maxCalls := 2
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"user": "u"}, MaxCalls: &maxCalls}); err != nil {
		t.Fatal(err)
	}
	host := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	counter := flowGet(t, host, flowCounter)
	if err := readyErr(t, counter); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := callMember(counter, "wait", nil, nil)
			done <- err
		}()
	}
	// Pi sends both waits before the add on one FIFO link; here both are seen in progress at the server first.
	waitFor(t, "two calls in progress", func() bool {
		return reviewOnLoop(server, func() bool {
			for _, connection := range server.connections.Keys() {
				if connection.incoming.Len() == 2 {
					return true
				}
			}
			return false
		})
	})
	_, err := callMember(counter, "add", float64(1), nil)
	var chordErr *ChordError
	if !IsChordError(err, CodeUnavailable) || !errors.As(err, &chordErr) || chordErr.Reason != "too_many_calls" {
		t.Fatalf("add beyond maxCalls: %v", err)
	}
	waiting.open()
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := addOne(t, counter); got != 1 {
		t.Fatalf("add = %v, want 1", got)
	}
}
