package host

// Ports packages/chord/test/links-review.test.ts: regression tests for the phase 1b links review (findings #1-#11)
// and the shared-worker test gap. Pi's awaits are blocking calls off the host loop. Where Pi waits a fixed time to
// let frames settle before asserting that something did not happen, the Go test waits on an observable sync point
// (a ping answered on the same link, the harness seeing the call arrive) or, where nothing observable follows, Pi's
// own settle time.

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// watchedLink is a link that logs what it sends and can drop a frame by closing instead (links-review.test.ts:37-66).
type watchedLink struct {
	inner   Link
	mu      sync.Mutex
	sent    []Frame
	closeOn func(frame Frame) bool
}

func newWatchedLink(inner Link) *watchedLink { return &watchedLink{inner: inner} }

func (link *watchedLink) Closed() bool { return link.inner.Closed() }

func (link *watchedLink) Send(frame Frame) (int, error) {
	link.mu.Lock()
	link.sent = append(link.sent, frame)
	closeOn := link.closeOn
	link.mu.Unlock()
	if closeOn != nil && closeOn(frame) {
		link.inner.Close("dropped")
		return 0, nil
	}
	return link.inner.Send(frame)
}

func (link *watchedLink) Receive(handler func(frame Frame))   { link.inner.Receive(handler) }
func (link *watchedLink) OnClose(handler func(reason string)) { link.inner.OnClose(handler) }
func (link *watchedLink) Close(reason ...string)              { link.inner.Close(reason...) }

func (link *watchedLink) setCloseOn(fn func(frame Frame) bool) {
	link.mu.Lock()
	link.closeOn = fn
	link.mu.Unlock()
}

// sentHold is the hold frame the link sent, if any.
func (link *watchedLink) sentHold() (HoldFrame, bool) {
	link.mu.Lock()
	defer link.mu.Unlock()
	for _, frame := range link.sent {
		if hold, ok := frame.(HoldFrame); ok {
			return hold, true
		}
	}
	return HoldFrame{}, false
}

func (link *watchedLink) sentType(t string) bool {
	link.mu.Lock()
	defer link.mu.Unlock()
	return slices.ContainsFunc(link.sent, func(frame Frame) bool { return frame.T() == t })
}

var reviewCounter = Remote("t.counter", RemoteDef{
	Version: 1,
	Input:   tObject("name", tString()),
	Host:    NewHostNamed("session:S1"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State: Fields[Schema]{{"count", linkCountSchema}},
		Methods: Fields[MethodSpec]{
			{"add", Method(tNumber(), tNumber())},
			{"who", Method(nil, tUnknown())},
			{"slow", Method(nil, nil)},
		},
	}}},
})

type reviewWorker struct {
	host *HostImpl
	link *watchedLink
}

type reviewBrowser struct {
	host *HostImpl
	up   *MemoryLink
}

// reviewCluster is a server routing session:* to workers it spawns once per name; the server→worker link is
// watched (links-review.test.ts:84-160).
type reviewCluster struct {
	t           *testing.T
	server      *HostImpl
	mu          sync.Mutex
	log         []string
	principals  []any
	workers     map[string]*reviewWorker
	resolveGate *requestsLatch
	slowGate    *requestsLatch
	// slowStarted is the harness's view of a slow call reaching the worker (Pi waits 10 ms for it).
	slowStarted chan struct{}
	// spawnMu makes resolve's check-and-spawn one section, as it is on Pi's single thread.
	spawnMu sync.Mutex
}

func (c *reviewCluster) push(entry string) {
	c.mu.Lock()
	c.log = append(c.log, entry)
	c.mu.Unlock()
}

func (c *reviewCluster) count(entry string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, logged := range c.log {
		if logged == entry {
			n++
		}
	}
	return n
}

func (c *reviewCluster) worker(name string) *reviewWorker {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workers[name]
}

func (c *reviewCluster) principalList() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.principals)
}

func (c *reviewCluster) browser(principal Principal, restamp func(Principal) (Principal, bool)) *reviewBrowser {
	c.t.Helper()
	down, up := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{Principal: principal, Restamp: restamp}); err != nil {
		c.t.Fatal(err)
	}
	return &reviewBrowser{host: requestsHost(c.t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}}), up: up}
}

type reviewRaw struct {
	raw    *MemoryLink
	frames *frameLog
	pings  int
}

func (c *reviewCluster) raw(principal Principal) *reviewRaw {
	c.t.Helper()
	down, raw := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{Principal: principal}); err != nil {
		c.t.Fatal(err)
	}
	frames := &frameLog{}
	raw.Receive(frames.add)
	_, _ = raw.Send(HelloFrame{Protocol: ProtocolVersion, Name: "raw", Kind: KindClient, CodeVersion: 0})
	return &reviewRaw{raw: raw, frames: frames}
}

// settle waits until the server answered a ping sent after everything before it: the link is FIFO, so every earlier
// frame was dispatched.
func (raw *reviewRaw) settle(t *testing.T) {
	t.Helper()
	raw.pings++
	n := raw.pings
	_, _ = raw.raw.Send(PingFrame{N: n})
	waitFor(t, "the pong", func() bool {
		return slices.ContainsFunc(raw.frames.list(), func(frame Frame) bool { return frame == Frame(PongFrame{N: n}) })
	})
}

func newReviewCluster(t *testing.T) *reviewCluster {
	c := &reviewCluster{t: t, workers: map[string]*reviewWorker{}, slowGate: newRequestsLatch(), slowStarted: make(chan struct{}, 16)}
	c.server = requestsHost(t, HostOptions{Name: "server"})
	c.server.Route("session:", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			c.mu.Lock()
			gate := c.resolveGate
			c.mu.Unlock()
			if gate != nil {
				gate.wait()
			}
			// Pi runs the rest of resolve as one synchronous section: two concurrent resolves never both spawn.
			c.spawnMu.Lock()
			defer c.spawnMu.Unlock()
			if existing := c.worker(name); existing != nil {
				return existing.link, nil
			}
			down, up := MemoryLinkPair()
			link := newWatchedLink(down)
			worker := requestsHost(t, HostOptions{Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
			// A slow call still waiting must finish before the worker closes (cleanups run last-in, first-out).
			t.Cleanup(c.slowGate.open)
			err := worker.Serve(reviewCounter, func(input any, principal Principal, self Self) (Implementation, error) {
				c.mu.Lock()
				c.principals = append(c.principals, principal)
				c.mu.Unlock()
				label := inputName(input)
				if _, err := self.Resource("probe", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { c.push("open " + label); return nil, nil },
					Close:   func(any) error { c.push("close " + label); return nil },
				}); err != nil {
					return nil, err
				}
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
					"who", methodFunc(func(any, Ctx) (any, error) { return principal, nil }),
					"slow", methodFunc(func(_ any, ctx Ctx) (any, error) {
						context.AfterFunc(ctx.Signal(), func() { c.push("slow aborted") })
						c.slowStarted <- struct{}{}
						c.slowGate.wait()
						return nil, nil
					}),
				), nil
			}, ServeOptions{})
			if err != nil {
				return nil, err
			}
			c.mu.Lock()
			c.workers[name] = &reviewWorker{host: worker, link: link}
			c.mu.Unlock()
			return link, nil
		},
	})
	return c
}

func (c *reviewCluster) setResolveGate(gate *requestsLatch) {
	c.mu.Lock()
	c.resolveGate = gate
	c.mu.Unlock()
}

// reviewConnection is the server's connection whose peer is named name, read on the loop.
func reviewConnection(host *HostImpl, name string) (connection *Connection) {
	host.run(func() {
		for _, candidate := range host.connections.Keys() {
			if candidate.peer.name != nil && *candidate.peer.name == name {
				connection = candidate
				return
			}
		}
	})
	return connection
}

func reviewOnLoop[T any](host *HostImpl, read func() T) (value T) {
	host.run(func() { value = read() })
	return value
}

func reviewReloading(host *HostImpl) bool {
	return reviewOnLoop(host, func() bool { return host.reloading })
}

func reviewGetCounter(t *testing.T, host *HostImpl, name string) Ref {
	t.Helper()
	return requestsGet(t, host, reviewCounter, map[string]any{"name": name}, nil)
}

// links-review.test.ts:163-172 "#1 the acceptor decides what a peer is: a client announcing itself as a host is cut
// off"
func TestLinksReview1TheAcceptorDecidesWhatAPeerIs(t *testing.T) {
	c := newReviewCluster(t)
	down, raw := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	raw.Receive(func(Frame) {})
	_, _ = raw.Send(HelloFrame{Name: "session:S9", Kind: KindHost, CodeVersion: 1})
	waitFor(t, "the raw link to close", raw.Closed)
	// A host link must have a name and its hello must match it.
	other, _ := MemoryLinkPair()
	err := c.server.Accept(other, AcceptOptions{Principal: map[string]any{}, Kind: KindHost})
	if err == nil || !strings.Contains(err.Error(), "needs its name") {
		t.Fatalf("accept of an unnamed host = %v", err)
	}
}

// reviewStateFacet is a facet serving typ with a cell under state version version.
func reviewStateFacet(typ *Contract, version int, implementation func() Implementation) *Facet {
	return &Facet{Name: "echo", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(typ, func(_ any, _ Principal, self Self) (Implementation, error) {
			if _, err := self.State("n", tNumber(), float64(0), &StateOptions{Version: version}); err != nil {
				return nil, err
			}
			return implementation(), nil
		}, ServeOptions{})
	}}}
}

// links-review.test.ts:174-215 "#1 root_end is honored only from the link the root came over"
func TestLinksReview1RootEndIsHonoredOnlyFromTheLinkTheRootCameOver(t *testing.T) {
	server := requestsHost(t, HostOptions{Name: "server"})
	echo := Remote("t.echo", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"root", Method(nil, tString())}},
	}}}})
	facet := func(version int) *Facet {
		return reviewStateFacet(echo, version, func() Implementation {
			return chordjson.ObjectOf("variant", "main", "root", methodFunc(func(_ any, ctx Ctx) (any, error) { return ctx.RootID(), nil }))
		})
	}
	if err := server.Install(*facet(1), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	down, up := MemoryLinkPair()
	name := "w"
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"worker": "w"}, Kind: KindHost, Name: &name}); err != nil {
		t.Fatal(err)
	}
	worker := requestsHost(t, HostOptions{Name: "w", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	gate := newRequestsLatch()
	rootIDs := make(chan string, 1)
	pinned := make(chan error, 1)
	go func() {
		_, err := worker.Root(func(ctx Ctx) (any, error) {
			ref, err := ctx.Get(echo, requestsEmpty(), nil)
			if err != nil {
				return nil, err
			}
			rootID, err := callMember(ref, "root", nil, ctx)
			if err != nil {
				return nil, err
			}
			rootIDs <- rootID.(string)
			gate.wait()
			return nil, nil
		}, RootOptions{})
		pinned <- err
	}()
	var rootID string
	select {
	case rootID = <-rootIDs:
	case err := <-pinned:
		t.Fatalf("pinned root ended early: %v", err)
	}
	// A client forging the worker root's end must not release its lease.
	clientDown, raw := MemoryLinkPair()
	if err := server.Accept(clientDown, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	client := &reviewRaw{raw: raw, frames: &frameLog{}}
	raw.Receive(client.frames.add)
	_, _ = raw.Send(HelloFrame{Protocol: ProtocolVersion, Name: "raw", Kind: KindClient, CodeVersion: 0})
	_, _ = raw.Send(RootEndFrame{RootID: rootID})
	client.settle(t)
	deadline := 40 * time.Millisecond
	_, err := server.Reload([]PluginRef{facet(2)}, ReloadOptions{GateDeadline: &deadline})
	var chordError *ChordError
	if !errors.As(err, &chordError) || chordError.Code != CodeAborted || chordError.Reason != "gate_deadline" || !strings.Contains(chordError.Message, rootID) {
		t.Fatalf("reload = %v", err)
	}
	gate.open()
	if err := <-pinned; err != nil {
		t.Fatal(err)
	}
}

// links-review.test.ts:217-231 "#2 a revocation while the route is still resolving closes the relay; no worker object
// is made"
func TestLinksReview2ARevocationWhileTheRouteIsStillResolvingClosesTheRelay(t *testing.T) {
	c := newReviewCluster(t)
	gate := newRequestsLatch()
	c.setResolveGate(gate)
	browser := c.browser(map[string]any{"guest": "g", "revision": float64(1)}, func(principal Principal) (Principal, bool) {
		next := map[string]any{}
		maps.Copy(next, principal.(map[string]any))
		next["revision"] = float64(2)
		return next, true
	})
	counter := reviewGetCounter(t, browser.host, "a")
	// Pi sleeps 5 ms for the hold to reach the server: wait until the server serves it as a relay.
	waitFor(t, "the relayed hold", func() bool {
		return reviewOnLoop(c.server, func() bool {
			for _, connection := range c.server.connections.Keys() {
				for _, entry := range connection.served.Values() {
					if !entry.local {
						return true
					}
				}
			}
			return false
		})
	})
	if err := c.server.InvalidatePrincipal(PrincipalMatch{Pattern: chordjson.ObjectOf("guest", "g")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "counter gone", func() bool { return counter.Status() == RefGone })
	gate.open()
	waitFor(t, "the route to resolve", func() bool { return c.worker("session:S1") != nil })
	time.Sleep(10 * time.Millisecond) // Pi's settle time before asserting that no object was made (links-review.test.ts:226).
	if got := c.principalList(); len(got) != 0 {
		t.Fatalf("principals = %s", jsonText(t, got))
	}
	again := reviewGetCounter(t, browser.host, "a")
	if got := jsonText(t, mustCall(t, again, "who", nil, nil)); got != `{"guest":"g","revision":2}` {
		t.Fatalf("who = %s", got)
	}
}

// links-review.test.ts:233-243 "#10 malformed frames and duplicate ids close the link"
func TestLinksReview10MalformedFramesAndDuplicateIDsCloseTheLink(t *testing.T) {
	c := newReviewCluster(t)
	first := c.raw(map[string]any{"guest": "g"})
	_, _ = first.raw.Send(MalformedFrame{Value: chordjson.ObjectOf("t", "call", "hold", float64(1))})
	waitFor(t, "the first link to close", first.raw.Closed)
	second := c.raw(map[string]any{"guest": "g"})
	token := reviewCounter.Description
	_, _ = second.raw.Send(HoldFrame{ID: 1, At: "session:S1", Token: token, Input: map[string]any{"name": "a"}})
	_, _ = second.raw.Send(HoldFrame{ID: 1, At: "session:S1", Token: token, Input: map[string]any{"name": "b"}})
	waitFor(t, "the second link to close", second.raw.Closed)
}

// links-review.test.ts:247-258 "#7 a browser disconnect cancels its call downstream"
func TestLinksReview7ABrowserDisconnectCancelsItsCallDownstream(t *testing.T) {
	c := newReviewCluster(t)
	browser := c.browser(map[string]any{"guest": "g"}, nil)
	counter := reviewGetCounter(t, browser.host, "a")
	if err := readyErr(t, counter); err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = callMember(counter, "slow", nil, nil) }()
	<-c.slowStarted
	browser.up.Close()
	waitFor(t, "slow aborted", func() bool { return c.count("slow aborted") == 1 })
	down := reviewConnection(c.server, "session:S1")
	if got := reviewOnLoop(c.server, down.relayedCalls.Len); got != 0 {
		t.Fatalf("relayed calls = %d", got)
	}
}

// links-review.test.ts:280-295 "#9 state stopped while the route resolves is forwarded as a hold without state"
func TestLinksReview9StateStoppedWhileTheRouteResolvesIsForwardedAsAHoldWithoutState(t *testing.T) {
	c := newReviewCluster(t)
	gate := newRequestsLatch()
	c.setResolveGate(gate)
	raw := c.raw(map[string]any{"guest": "g"})
	_, _ = raw.raw.Send(HoldFrame{ID: 1, At: "session:S1", Token: reviewCounter.Description, Input: map[string]any{"name": "a"}})
	_, _ = raw.raw.Send(UnsubFrame{Hold: 1})
	raw.settle(t)
	gate.open()
	waitFor(t, "the forwarded hold", func() bool {
		worker := c.worker("session:S1")
		return worker != nil && worker.link.sentType("hold")
	})
	time.Sleep(10 * time.Millisecond)
	link := c.worker("session:S1").link
	if hold, _ := link.sentHold(); !hold.NoState {
		t.Fatalf("the forwarded hold %+v wants state", hold)
	}
	if link.sentType("sub") || link.sentType("unsub") {
		t.Fatal("a sub or unsub was forwarded")
	}
}

// links-review.test.ts:274-288 "#9 a release while the route resolves settles waiting calls and forwards nothing"
func TestLinksReview9AReleaseWhileTheRouteResolvesSettlesWaitingCallsAndForwardsNothing(t *testing.T) {
	c := newReviewCluster(t)
	gate := newRequestsLatch()
	c.setResolveGate(gate)
	raw := c.raw(map[string]any{"guest": "g"})
	_, _ = raw.raw.Send(HoldFrame{ID: 1, At: "session:S1", Token: reviewCounter.Description, Input: map[string]any{"name": "a"}})
	_, _ = raw.raw.Send(CallFrame{ID: 2, Hold: 1, Member: "add", Args: float64(1), HasArgs: true})
	raw.settle(t)
	_, _ = raw.raw.Send(ReleaseFrame{ID: 1})
	var result *ResultFrame
	waitFor(t, "the call's result", func() bool {
		for _, frame := range raw.frames.list() {
			if frame, ok := frame.(ResultFrame); ok && frame.ID == 2 {
				result = &frame
				return true
			}
		}
		return false
	})
	if result.Error == nil {
		t.Fatalf("result = %+v, want an error", result)
	}
	gate.open()
	waitFor(t, "the route to resolve", func() bool { return c.worker("session:S1") != nil })
	time.Sleep(10 * time.Millisecond) // Pi's settle time before asserting that nothing was forwarded (links-review.test.ts:286).
	if c.worker("session:S1").link.sentType("hold") {
		t.Fatal("a released hold was forwarded")
	}
}

// links-review.test.ts:290-305 "#9 an origin disconnect removes its relayed holds and subscriptions downstream"
func TestLinksReview9AnOriginDisconnectRemovesItsRelayedRefsAndSubscriptionsDownstream(t *testing.T) {
	c := newReviewCluster(t)
	browser := c.browser(map[string]any{"guest": "g"}, nil)
	counter := reviewGetCounter(t, browser.host, "a")
	requestsReadable(t, counter, "count").Subscribe(func(any) {})
	mustCall(t, counter, "add", float64(1), nil)
	down := reviewConnection(c.server, "session:S1")
	if got := reviewOnLoop(c.server, down.relayedRefs.Len); got != 1 {
		t.Fatalf("relayed refs = %d", got)
	}
	browser.up.Close()
	waitFor(t, "relays removed", func() bool {
		return reviewOnLoop(c.server, func() bool { return down.relayedRefs.Len() == 0 })
	})
	waitFor(t, "close a", func() bool { return c.count("close a") == 1 })
}

// links-review.test.ts:307-320 "two browsers share one worker connection; one leaving releases only its own holds"
func TestLinksReviewTwoBrowsersShareOneWorkerConnection(t *testing.T) {
	c := newReviewCluster(t)
	one := c.browser(map[string]any{"guest": "g1"}, nil)
	two := c.browser(map[string]any{"guest": "g2"}, nil)
	a := reviewGetCounter(t, one.host, "same")
	b := reviewGetCounter(t, two.host, "same")
	if got := mustCall(t, a, "add", float64(1), nil); got != float64(1) {
		t.Fatalf("a.add = %v", got)
	}
	if got := mustCall(t, b, "add", float64(5), nil); got != float64(5) {
		t.Fatalf("b.add = %v", got)
	}
	c.mu.Lock()
	workers := len(c.workers)
	c.mu.Unlock()
	if workers != 1 {
		t.Fatalf("workers = %d", workers)
	}
	if got := jsonText(t, c.principalList()); got != `[{"guest":"g1"},{"guest":"g2"}]` {
		t.Fatalf("principals = %s", got)
	}
	one.up.Close()
	waitFor(t, "close same", func() bool { return c.count("close same") == 1 })
	if got := mustCall(t, b, "add", float64(1), nil); got != float64(6) {
		t.Fatalf("b.add after = %v", got)
	}
}

// mappedView is the accepts adapter's view of a v2 state member as v1's (links-review.test.ts:340-355).
type mappedView struct{ view Readable[any] }

func mapView(value any) any {
	object, _ := value.(*chordjson.Object)
	if object == nil {
		return nil
	}
	return chordjson.ObjectOf("text", object.Value("body"))
}

func (view mappedView) Value() any { return mapView(view.view.Value()) }

func (view mappedView) Subscribe(fn func(value any)) func() {
	return view.view.Events(func(StateEvent[any]) { fn(mapView(view.view.Value())) })
}

func (view mappedView) Events(fn func(event StateEvent[any])) func() {
	return view.view.Events(func(event StateEvent[any]) {
		kind := event.Type
		if kind == StateEventOps {
			kind = StateEventReset
		}
		fn(StateEvent[any]{Type: kind, Value: mapView(view.view.Value())})
	})
}

// links-review.test.ts:322-365 "#11 snapshots through an accepts adapter are validated against the consumer's
// schema"
func TestLinksReview11SnapshotsThroughAnAcceptsAdapterAreValidatedAgainstTheConsumersSchema(t *testing.T) {
	docV1 := Remote("t.doc", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		State: Fields[Schema]{{"view", tObject("text", tString())}},
	}}}})
	docV2 := Remote("t.doc", RemoteDef{Version: 2, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		State: Fields[Schema]{{"view", tObject("body", tString())}},
	}}}})
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, docV2, func(_ any, _ Principal, self Self) (Implementation, error) {
		state, err := self.State("view", tObject("body", tString()), delta.JsonObjectOf("body", "hi"), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "view", state), nil
	}, ServeOptions{Accepts: Accepts{1: func(current Implementation) Implementation {
		return chordjson.ObjectOf("variant", "main", "view", mappedView{view: current.Value("view").(Readable[any])})
	}}})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	doc := requestsGet(t, browser, docV1, requestsEmpty(), nil)
	if err := readyErr(t, doc); err != nil {
		t.Fatal(err)
	}
	view := requestsReadable(t, doc, "view")
	if got := jsonText(t, view.Value()); got != `{"text":"hi"}` {
		t.Fatalf("view = %s", got)
	}
}

var reviewGreeter = Remote("t.greeter", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("session:S1"), Variants: Fields[VariantDef]{{"main", VariantDef{
	Methods: Fields[MethodSpec]{{"greet", Method(tString(), tString())}},
}}}})

func reviewGreeterFacet(greeting string) Facet {
	return Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(reviewGreeter, func(any, Principal, Self) (Implementation, error) {
			return chordjson.ObjectOf("variant", "main", "greet", methodFunc(func(name any, _ Ctx) (any, error) {
				return greeting + " " + name.(string), nil
			})), nil
		}, ServeOptions{})
	}}}
}

type reviewReloadSetup struct {
	server, worker *HostImpl
	modulesMu      sync.Mutex
	modules        map[string]Facet
	link           *watchedLink
	// loading receives once per loader call (Pi waits 5 ms for the child to start loading).
	loading chan struct{}
}

func (setup *reviewReloadSetup) setModule(name string, facet Facet) {
	setup.modulesMu.Lock()
	setup.modules[name] = facet
	setup.modulesMu.Unlock()
}

func (setup *reviewReloadSetup) connect(t *testing.T) (*watchedLink, *MemoryLink) {
	t.Helper()
	down, up := MemoryLinkPair()
	watched := newWatchedLink(down)
	name := "session:S1"
	if err := setup.server.Accept(watched, AcceptOptions{Principal: map[string]any{"worker": "w"}, Kind: KindHost, Name: &name}); err != nil {
		t.Fatal(err)
	}
	return watched, up
}

// newReviewReloadSetup is links-review.test.ts:375-394 setup(options).
func newReviewReloadSetup(t *testing.T, slowLoad *requestsLatch) *reviewReloadSetup {
	setup := &reviewReloadSetup{modules: map[string]Facet{"greeter": reviewGreeterFacet("v1")}, loading: make(chan struct{}, 16)}
	loader := func(names []string) ([]Facet, error) {
		setup.loading <- struct{}{}
		if slowLoad != nil {
			slowLoad.wait()
		}
		setup.modulesMu.Lock()
		defer setup.modulesMu.Unlock()
		facets := make([]Facet, len(names))
		for index, name := range names {
			facets[index] = setup.modules[name]
		}
		return facets, nil
	}
	setup.server = requestsHost(t, HostOptions{Name: "server"})
	link, up := setup.connect(t)
	setup.link = link
	setup.worker = requestsHost(t, HostOptions{Name: "session:S1", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, Loader: loader})
	setup.modulesMu.Lock()
	greeter := setup.modules["greeter"]
	setup.modulesMu.Unlock()
	if err := setup.worker.Install(greeter, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	return setup
}

// reviewSynced waits until the server can reach the worker (Pi's tick after setup).
func reviewSynced(t *testing.T, server *HostImpl) {
	t.Helper()
	waitFor(t, "the worker to be synced", func() bool {
		connection := reviewConnection(server, "session:S1")
		return connection != nil && reviewOnLoop(server, func() bool { return connection.synced })
	})
}

var reviewRemote = map[string][]string{"session:": {"greeter"}}

// links-review.test.ts:396-409 "#3 an abort while the child is still loading modules frees its reload slot; a later
// reload works"
func TestLinksReview3AnAbortWhileTheChildIsStillLoadingModulesFreesItsReloadSlot(t *testing.T) {
	gate := newRequestsLatch()
	setup := newReviewReloadSetup(t, gate)
	t.Cleanup(gate.open)
	reviewSynced(t, setup.server)
	setup.setModule("greeter", reviewGreeterFacet("v2"))
	voteDeadline := 30 * time.Millisecond
	if _, err := setup.server.Reload(nil, ReloadOptions{Remote: reviewRemote, VoteDeadline: &voteDeadline}); requestsChordCode(err) != CodeAborted {
		t.Fatalf("reload = %v", err)
	}
	gate.open()
	waitFor(t, "the child's reload slot", func() bool { return !reviewReloading(setup.worker) })
	if _, err := setup.server.Reload(nil, ReloadOptions{Remote: reviewRemote}); err != nil {
		t.Fatal(err)
	}
	if got := mustCall(t, requestsGet(t, setup.server, reviewGreeter, requestsEmpty(), nil), "greet", "x", nil); got != "v2 x" {
		t.Fatalf("greet = %v", got)
	}
}

// links-review.test.ts:411-423 "#3 the parent link dying while the child prepares unwinds the child"
func TestLinksReview3TheParentLinkDyingWhileTheChildPreparesUnwindsTheChild(t *testing.T) {
	gate := newRequestsLatch()
	setup := newReviewReloadSetup(t, gate)
	t.Cleanup(gate.open)
	reviewSynced(t, setup.server)
	setup.setModule("greeter", reviewGreeterFacet("v2"))
	reload := make(chan error, 1)
	go func() {
		_, err := setup.server.Reload(nil, ReloadOptions{Remote: reviewRemote})
		reload <- err
	}()
	<-setup.loading
	setup.link.Close("gone")
	if err := <-reload; err == nil {
		t.Fatal("reload succeeded without its child")
	}
	gate.open()
	waitFor(t, "the child to unwind", func() bool { return !reviewReloading(setup.worker) })
	if got := setup.worker.CodeVersion(); got != 1 {
		t.Fatalf("worker code version = %d", got)
	}
}

// links-review.test.ts:425-458 "#4 tasks the commit starts cannot reach a child before the child's commit"
func TestLinksReview4TasksTheCommitStartsCannotReachAChildBeforeTheChildsCommit(t *testing.T) {
	setup := newReviewReloadSetup(t, nil)
	reviewSynced(t, setup.server)
	var mu sync.Mutex
	var results []any
	caller := Local("t.caller", LocalDef{Version: 1, Input: tObject()})
	callerFacet := func(active bool) *Facet {
		return &Facet{Name: "caller", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(caller, func(_ any, _ Principal, self Self) (Implementation, error) {
				greeter, err := self.Get(reviewGreeter, requestsEmpty(), nil)
				if err != nil {
					return nil, err
				}
				if active {
					if err := self.Task(func(ctx Ctx) error {
						value, err := callMember(greeter, "greet", "t", ctx)
						if err != nil {
							value = string(requestsChordCode(err))
						}
						mu.Lock()
						results = append(results, value)
						mu.Unlock()
						return nil
					}); err != nil {
						return nil, err
					}
				}
				return chordjson.ObjectOf(), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	if err := setup.server.Install(*callerFacet(false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	setup.setModule("greeter", reviewGreeterFacet("v2"))
	if _, err := setup.server.Reload([]PluginRef{callerFacet(true)}, ReloadOptions{Remote: reviewRemote}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task's call", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(results) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(results, []any{"v2 t"}) {
		t.Fatalf("results = %v", results)
	}
}

// links-review.test.ts:460-474 "#5 a child that missed a commit is reconciled on reconnect, never relabeled"
func TestLinksReview5AChildThatMissedACommitIsReconciledOnReconnect(t *testing.T) {
	setup := newReviewReloadSetup(t, nil)
	reviewSynced(t, setup.server)
	setup.setModule("greeter", reviewGreeterFacet("v2"))
	// The commit frame is lost: the link dies right after the child voted.
	setup.link.setCloseOn(func(frame Frame) bool { return frame.T() == "commit" })
	if _, err := setup.server.Reload(nil, ReloadOptions{Remote: reviewRemote}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the child's reload to end", func() bool { return !reviewReloading(setup.worker) })
	if setup.worker.CodeVersion() == setup.server.CodeVersion() {
		t.Fatalf("worker code version %d equals the server's", setup.worker.CodeVersion())
	}
	_, up := setup.connect(t)
	setup.worker.Connect(up, ConnectOptions{Principal: kindServer()})
	greeter := requestsGet(t, setup.server, reviewGreeter, requestsEmpty(), nil)
	if got := mustCall(t, greeter, "greet", "x", nil); got != "v2 x" {
		t.Fatalf("greet = %v", got)
	}
	if setup.worker.CodeVersion() != setup.server.CodeVersion() {
		t.Fatalf("worker code version %d, server code version %d", setup.worker.CodeVersion(), setup.server.CodeVersion())
	}
}

// links-review.test.ts:476-526 "#6 a server root that bounces through a worker and back ends normally (no leaked
// lease)"
func TestLinksReview6AServerRootThatBouncesThroughAWorkerAndBackEndsNormally(t *testing.T) {
	setup := newReviewReloadSetup(t, nil)
	reviewSynced(t, setup.server)
	echo := Remote("t.echo2", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"ping", Method(nil, nil)}},
	}}}})
	bouncer := Remote("t.bouncer", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("session:S1"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"bounce", Method(nil, nil)}},
	}}}})
	echoFacet := func(version int) *Facet {
		return reviewStateFacet(echo, version, func() Implementation {
			return chordjson.ObjectOf("variant", "main", "ping", methodFunc(func(any, Ctx) (any, error) { return nil, nil }))
		})
	}
	if err := setup.server.Install(*echoFacet(1), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	// Keeps the Echo object the bounced call reaches (it acts as the worker's principal) alive, so the reload gate
	// must decide about the bounced root's lease.
	held := requestsGet(t, setup.server, echo, requestsEmpty(), &GetOptions{Principal: map[string]any{"worker": "w"}, HasPrincipal: true})
	if err := readyErr(t, held); err != nil {
		t.Fatal(err)
	}
	requestsServe(t, setup.worker.Serve, bouncer, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "bounce", methodFunc(func(_ any, ctx Ctx) (any, error) {
			ref, err := ctx.Get(echo, requestsEmpty(), nil)
			if err != nil {
				return nil, err
			}
			_, err = callMember(ref, "ping", nil, ctx)
			return nil, err
		})), nil
	}, ServeOptions{})
	if _, err := setup.server.Root(func(ctx Ctx) (any, error) {
		ref, err := ctx.Get(bouncer, requestsEmpty(), nil)
		if err != nil {
			return nil, err
		}
		return callMember(ref, "bounce", nil, ctx)
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond) // Pi's settle time for the bounced root's root_end (links-review.test.ts:523).
	gateDeadline := 200 * time.Millisecond
	if _, err := setup.server.Reload([]PluginRef{echoFacet(2)}, ReloadOptions{GateDeadline: &gateDeadline}); err != nil {
		t.Fatal(err)
	}
}
