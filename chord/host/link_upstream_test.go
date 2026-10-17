package host

// Ports packages/chord/test/link.test.ts: calls, state, relays, principal stamping and link lifetime across hosts
// connected by memory links. These tests drive the whole host runtime (host.ts, cell.ts, connection.ts, object.ts,
// remote.ts); they run at the combined gate.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var linkCountSchema = tObject("n", tNumber())

// linkCounterVariants is link.test.ts counterVariants.
var linkCounterVariants = Fields[VariantDef]{
	{"main", VariantDef{
		State: Fields[Schema]{{"count", linkCountSchema}},
		Methods: Fields[MethodSpec]{
			{"add", Method(tNumber(), tNumber())},
			{"who", Method(nil, tUnknown())},
		},
	}},
}

var (
	// linkCounter is link.test.ts Counter: a counter on the server.
	linkCounter = Remote("t.counter", RemoteDef{
		Version:  1,
		Input:    tObject("name", tString()),
		Host:     NewHostNamed("server"),
		Variants: linkCounterVariants,
	})
	// linkSessionCounter is link.test.ts SessionCounter: a counter on a session host (`session:` + its session field).
	linkSessionCounter = Remote("t.session-counter", RemoteDef{
		Version:  1,
		Input:    tObject("session", tString(), "name", tString()),
		Host:     NewHostFamily("session:", "session"),
		Variants: linkCounterVariants,
	})
)

type principalLog struct {
	mu   sync.Mutex
	list []any
}

func (log *principalLog) add(principal any) {
	log.mu.Lock()
	log.list = append(log.list, principal)
	log.mu.Unlock()
}

func (log *principalLog) items() []any {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.list)
}

// counterFactory is the Counter factory of serveCounter and the cluster's workers; onCreate sees each object's
// principal and self.
func counterFactory(onCreate func(input any, principal Principal, self Self) error) Factory {
	return func(input any, principal Principal, self Self) (Implementation, error) {
		if onCreate != nil {
			if err := onCreate(input, principal, self); err != nil {
				return nil, err
			}
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
		), nil
	}
}

func countN(value any) float64 {
	switch value := value.(type) {
	case map[string]any:
		return value["n"].(float64)
	case *chordjson.Object:
		return value.Value("n").(float64)
	}
	return -1
}

// serveCounter serves the counter of the host's kind (Counter on the server, SessionCounter elsewhere) and records the
// principal of every object it creates.
func serveCounter(t *testing.T, host *HostImpl, principals *principalLog) {
	t.Helper()
	typ := linkSessionCounter
	if host.Name() == "server" {
		typ = linkCounter
	}
	if err := host.Serve(typ, counterFactory(func(_ any, principal Principal, _ Self) error {
		if principals != nil {
			principals.add(principal)
		}
		return nil
	}), ServeOptions{}); err != nil {
		t.Fatal(err)
	}
}

type workerPair struct {
	server, worker *HostImpl
	down, up       *MemoryLink
}

// serverWithWorker is a server and a worker connected by a memory link; the server accepts the worker as
// { worker: name }.
func serverWithWorker(t *testing.T, workerName string) workerPair {
	t.Helper()
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"worker": workerName}, Kind: "host", Name: &workerName}); err != nil {
		t.Fatal(err)
	}
	worker := CreateHost(HostOptions{Self: kindHost(), Name: workerName, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = worker.Close(); _ = server.Close() })
	return workerPair{server: server, worker: worker, down: down, up: up}
}

// mustGet gets the server's counter named name.
func mustGet(t *testing.T, host *HostImpl, name string) Ref {
	t.Helper()
	ref, err := host.Hold(linkCounter, map[string]any{"name": name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// mustGetSession gets the counter named name on session host `session:`+session.
func mustGetSession(t *testing.T, host *HostImpl, session, name string) Ref {
	t.Helper()
	ref, err := host.Hold(linkSessionCounter, map[string]any{"session": session, "name": name}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func callMember(ref Ref, name string, args any, ctx Ctx) (any, error) {
	method, ok := ref.Member(name).(methodFunc)
	if !ok {
		return nil, errors.New(name + " is not a method")
	}
	return method(args, ctx)
}

func mustCall(t *testing.T, ref Ref, name string, args any, ctx Ctx) any {
	t.Helper()
	value, err := callMember(ref, name, args, ctx)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return value
}

func readyErr(t *testing.T, ref Ref) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return ref.Ready(ctx)
}

func countReadable(t *testing.T, ref Ref) Readable[any] {
	t.Helper()
	readable, ok := ref.Member("count").(Readable[any])
	if !ok {
		t.Fatal("count is not state")
	}
	return readable
}

// link.test.ts:66-78
func TestLinksAChildCallsTheServerStampedWithItsPrincipal(t *testing.T) {
	pair := serverWithWorker(t, "session:S1")
	var principals principalLog
	serveCounter(t, pair.server, &principals)
	result, err := pair.worker.Root(func(ctx Ctx) (any, error) {
		counter, err := ctx.Get(linkCounter, map[string]any{"name": "c"}, nil)
		if err != nil {
			return nil, err
		}
		if err := readyErr(t, counter); err != nil {
			return nil, err
		}
		if kind := counter.Variant(); kind == nil || *kind != "main" {
			t.Errorf("kind = %v", kind)
		}
		added, err := callMember(counter, "add", float64(2), ctx)
		if err != nil {
			return nil, err
		}
		who, err := callMember(counter, "who", nil, ctx)
		return []any{added, who}, err
	}, RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonText(t, result); got != `[2,{"worker":"session:S1"}]` {
		t.Fatalf("result = %s", got)
	}
	if got := jsonText(t, principals.items()); got != `[{"worker":"session:S1"}]` {
		t.Fatalf("principals = %s", got)
	}
}

// link.test.ts:80-98
func TestLinksStateMembersSnapshotThenDeltaOps(t *testing.T) {
	pair := serverWithWorker(t, "session:S1")
	serveCounter(t, pair.server, nil)
	counter := mustGet(t, pair.worker, "s")
	count := countReadable(t, counter)
	var mu sync.Mutex
	var events []string
	var values []string
	count.Events(func(event StateEvent[any]) {
		mu.Lock()
		events = append(events, string(event.Type))
		mu.Unlock()
	})
	count.Subscribe(func(value any) {
		mu.Lock()
		values = append(values, jsonText(t, value))
		mu.Unlock()
	})
	if err := readyErr(t, counter); err != nil {
		t.Fatal(err)
	}
	mustCall(t, counter, "add", float64(1), nil)
	mustCall(t, counter, "add", float64(1), nil)
	waitFor(t, "two ops", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) == 3
	})
	if got := jsonText(t, count.Value()); got != `{"n":2}` {
		t.Fatalf("value = %s", got)
	}
	mu.Lock()
	defer mu.Unlock()
	// Listeners added before the hydration get the snapshot then (not undefined first).
	if !slices.Equal(events, []string{"snap", "ops", "ops"}) {
		t.Fatalf("events = %v", events)
	}
	if !slices.Equal(values, []string{`{"n":0}`, `{"n":1}`, `{"n":2}`}) {
		t.Fatalf("values = %v", values)
	}
}

// link.test.ts:100-109
func TestLinksTheServerReachesAConnectedChildByName(t *testing.T) {
	pair := serverWithWorker(t, "session:w1")
	var principals principalLog
	serveCounter(t, pair.worker, &principals)
	counter := mustGetSession(t, pair.server, "w1", "x")
	if added := mustCall(t, counter, "add", float64(5), nil); added != float64(5) {
		t.Fatalf("add = %v", added)
	}
	// The parent's own requests act as the principal the child gave connect.
	if got := jsonText(t, principals.items()); got != `[{"kind":"server"}]` {
		t.Fatalf("principals = %s", got)
	}
}

// link.test.ts:111-115
func TestLinksRejectUnknownHosts(t *testing.T) {
	pair := serverWithWorker(t, "session:S1")
	missing := mustGet(t, pair.worker, "m")
	if err := readyErr(t, missing); !IsChordError(err, CodeUnavailable) {
		t.Fatalf("ready = %v", err)
	}
}

type linkCluster struct {
	t       *testing.T
	server  *HostImpl
	mu      sync.Mutex
	workers map[string]*HostImpl
	log     []string
	allow   func(principal Principal, host string) bool
	options LinkOptions
}

type browserHost struct {
	host      *HostImpl
	up        *MemoryLink
	reconnect func()
}

func (c *linkCluster) logged(entry string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.log, entry)
}

func (c *linkCluster) workerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.workers)
}

func (c *linkCluster) browser(principal Principal, restamp func(Principal) (Principal, bool)) *browserHost {
	t := c.t
	down, up := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{LinkOptions: c.options, Principal: principal, Restamp: restamp}); err != nil {
		t.Fatal(err)
	}
	host := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: "client", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{LinkOptions: c.options, Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = host.Close() })
	browser := &browserHost{host: host, up: up}
	browser.reconnect = func() {
		nextDown, nextUp := MemoryLinkPair()
		if err := c.server.Accept(nextDown, AcceptOptions{Principal: principal}); err != nil {
			t.Fatal(err)
		}
		browser.up = nextUp
		host.Connect(nextUp, ConnectOptions{Principal: kindServer()})
	}
	return browser
}

// cluster is a server routing session:* to workers it spawns on demand (memory links); browsers connect as clients.
func cluster(t *testing.T, options LinkOptions) *linkCluster {
	c := &linkCluster{t: t, workers: map[string]*HostImpl{}, options: options}
	c.allow = func(Principal, string) bool { return true }
	c.server = CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = c.server.Close() })
	c.server.Route("session:", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(principal Principal, host string) (bool, error) { return c.allow(principal, host), nil },
		Resolve: func(name string) (Link, error) {
			down, up := MemoryLinkPair()
			worker := CreateHost(HostOptions{Self: kindHost(), Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{LinkOptions: options, Principal: kindServer()}}, OnError: func(error) {}})
			t.Cleanup(func() { _ = worker.Close() })
			err := worker.Serve(linkSessionCounter, counterFactory(func(input any, _ Principal, self Self) error {
				name := inputName(input)
				_, err := self.Resource("probe", ResourceDeclaration[any]{
					Version: 1,
					Open: func(ResourceOpenOptions) (any, error) {
						c.mu.Lock()
						c.log = append(c.log, "open "+name)
						c.mu.Unlock()
						return nil, nil
					},
					Close: func(any) error {
						c.mu.Lock()
						c.log = append(c.log, "close "+name)
						c.mu.Unlock()
						return nil
					},
				})
				return err
			}), ServeOptions{})
			if err != nil {
				return nil, err
			}
			c.mu.Lock()
			c.workers[name] = worker
			c.mu.Unlock()
			return down, nil
		},
	})
	return c
}

func inputName(input any) string {
	switch input := input.(type) {
	case map[string]any:
		return input["name"].(string)
	case *chordjson.Object:
		return input.Value("name").(string)
	}
	return ""
}

// link.test.ts:197-209
func TestLinksRelayBrowserServerWorkerWithTheStampedPrincipal(t *testing.T) {
	c := cluster(t, LinkOptions{})
	guest := map[string]any{"guest": "g1"}
	browser := c.browser(guest, nil)
	counter := mustGetSession(t, browser.host, "S1", "a")
	if got := mustCall(t, counter, "add", float64(3), nil); got != float64(3) {
		t.Fatalf("add = %v", got)
	}
	if got := jsonText(t, mustCall(t, counter, "who", nil, nil)); got != `{"guest":"g1"}` {
		t.Fatalf("who = %s", got)
	}
	var mu sync.Mutex
	var last string
	countReadable(t, counter).Subscribe(func(value any) {
		mu.Lock()
		last = jsonText(t, value)
		mu.Unlock()
	})
	mustCall(t, counter, "add", float64(1), nil)
	waitFor(t, "n = 4", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return last == `{"n":4}`
	})
}

// rawResult waits for the result frame of a raw link and returns its value.
func rawResult(t *testing.T, frames *frameLog) any {
	t.Helper()
	var result *ResultFrame
	waitFor(t, "a result frame", func() bool {
		for _, frame := range frames.list() {
			if frame, ok := frame.(ResultFrame); ok {
				result = &frame
				return true
			}
		}
		return false
	})
	return result.Value
}

// link.test.ts:211-249
func TestLinksIgnorePayloadPrincipalsFromClientsAndChildHosts(t *testing.T) {
	c := cluster(t, LinkOptions{})
	down, raw := MemoryLinkPair()
	if err := c.server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g1"}}); err != nil {
		t.Fatal(err)
	}
	var frames frameLog
	raw.Receive(frames.add)
	owner := map[string]any{"owner": true}
	_, _ = raw.Send(HelloFrame{Protocol: ProtocolVersion, Name: "raw", Kind: "client", CodeVersion: 0})
	_, _ = raw.Send(HoldFrame{ID: 1, At: "session:S1", Token: linkSessionCounter.Description, Input: map[string]any{"session": "S1", "name": "f"}, Principal: owner, HasPrincipal: true})
	_, _ = raw.Send(CallFrame{ID: 2, Hold: 1, Member: "who", Args: nil})
	if got := jsonText(t, rawResult(t, &frames)); got != `{"guest":"g1"}` {
		t.Fatalf("client result = %s", got)
	}

	// A child host cannot forward principals upward either: the server stamps it.
	pair := serverWithWorker(t, "w9")
	serveCounter(t, pair.server, nil)
	var childFrames frameLog
	pair.up.Receive(childFrames.add)
	_, _ = pair.up.Send(HoldFrame{ID: 7, At: "server", Token: linkCounter.Description, Input: map[string]any{"name": "f"}, Principal: owner, HasPrincipal: true})
	_, _ = pair.up.Send(CallFrame{ID: 8, Hold: 7, Member: "who", Args: nil})
	if got := jsonText(t, rawResult(t, &childFrames)); got != `{"worker":"w9"}` {
		t.Fatalf("child result = %s", got)
	}
}

// link.test.ts (Pi 7f022043) "a route that returns an already closed link does not poison the host name"
func TestLinksARouteThatReturnsAnAlreadyClosedLinkDoesNotPoisonTheHostName(t *testing.T) {
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	var mu sync.Mutex
	attempt := 0
	server.Route("session:", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			mu.Lock()
			attempt++
			first := attempt == 1
			mu.Unlock()
			down, up := MemoryLinkPair()
			worker := CreateHost(HostOptions{Self: kindHost(), Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
			t.Cleanup(func() { _ = worker.Close() })
			serveCounter(t, worker, nil)
			// The first worker dies before the server wraps its link.
			if first {
				down.Close("worker exited")
			}
			return down, nil
		},
	})
	first := mustGetSession(t, server, "S1", "a")
	if _, err := callMember(first, "add", float64(1), nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("first add = %v, want a closed error", err)
	}
	// The next call resolves the route again and reaches the new worker.
	second := mustGetSession(t, server, "S1", "a")
	if got := mustCall(t, second, "add", float64(2), nil); got != float64(2) {
		t.Fatalf("second add = %v", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempt != 2 {
		t.Fatalf("resolve attempts = %d", attempt)
	}
}

// connection.ts (Pi 7f022043): a connection whose link was already closed is not kept and starts no ping timer.
func TestLinksAnAlreadyClosedLinkIsNotKeptAndStartsNoPinger(t *testing.T) {
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	down, _ := MemoryLinkPair()
	down.Close("gone")
	if err := server.Accept(down, AcceptOptions{}); err != nil {
		t.Fatal(err)
	}
	var kept int
	var pinger bool
	server.run(func() {
		kept = server.connections.Len()
		other, _ := MemoryLinkPair()
		other.Close("gone")
		pinger = newConnection(server, other, connectionConfig{role: "downstream", peer: Peer{kind: KindClient}}).stopPinger != nil
	})
	if kept != 0 {
		t.Fatalf("connections kept = %d, want 0", kept)
	}
	if pinger {
		t.Fatal("a connection on a closed link started its ping timer")
	}
}

// link.test.ts:251-266
func TestLinksAuthorizeBeforeResolvingAndAgainAfter(t *testing.T) {
	c := cluster(t, LinkOptions{})
	c.allow = func(principal Principal, _ string) bool { return principal.(map[string]any)["guest"] != "blocked" }
	blocked := c.browser(map[string]any{"guest": "blocked"}, nil)
	if err := readyErr(t, mustGetSession(t, blocked.host, "S1", "a")); !IsChordError(err, CodeForbidden) {
		t.Fatalf("blocked ready = %v", err)
	}
	if c.workerCount() != 0 {
		t.Fatalf("workers = %d", c.workerCount())
	}
	var mu sync.Mutex
	calls := 0
	c.allow = func(Principal, string) bool {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return calls == 1
	}
	flaky := c.browser(map[string]any{"guest": "flaky"}, nil)
	if err := readyErr(t, mustGetSession(t, flaky.host, "S2", "a")); !IsChordError(err, CodeForbidden) {
		t.Fatalf("flaky ready = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("authorize calls = %d", calls)
	}
}

// link.test.ts:268-280
func TestLinksABrowserDisconnectReleasesExactlyItsRefs(t *testing.T) {
	c := cluster(t, LinkOptions{})
	one := c.browser(map[string]any{"guest": "g1"}, nil)
	two := c.browser(map[string]any{"guest": "g2"}, nil)
	mustCall(t, mustGetSession(t, one.host, "S1", "a"), "add", float64(1), nil)
	other := mustGetSession(t, two.host, "S1", "b")
	mustCall(t, other, "add", float64(1), nil)
	one.up.Close()
	waitFor(t, "close a", func() bool { return c.logged("close a") })
	if c.logged("close b") {
		t.Fatal("b closed with a's browser")
	}
	if got := mustCall(t, other, "add", float64(1), nil); got != float64(2) {
		t.Fatalf("add = %v", got)
	}
}

// link.test.ts:282-289
func TestLinksADeadLinkIsClosedAndReleasesEverythingHeld(t *testing.T) {
	// Pi uses a 40 ms ping timeout. In a loaded Go test process a 40 ms gap in pong delivery closes the link before the first
	// call, and the client's ref then waits for a reconnect that never comes (detached, remote.ts:276-286), so the test hangs.
	// One second keeps the link up until Sever, and the dead link is still found well within waitFor's deadline.
	interval, timeout := 10*time.Millisecond, time.Second
	c := cluster(t, LinkOptions{PingInterval: &interval, PingTimeout: &timeout})
	browser := c.browser(map[string]any{"guest": "g1"}, nil)
	mustCall(t, mustGetSession(t, browser.host, "S1", "a"), "add", float64(1), nil)
	browser.up.Sever()
	waitFor(t, "close a", func() bool { return c.logged("close a") })
}

// link.test.ts:291-308
func TestLinksCheckContractTokensAcrossLinks(t *testing.T) {
	counterV2 := Remote("t.counter", RemoteDef{
		Version:  2,
		Input:    tObject("name", tString()),
		Host:     NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{{"inc", Method(nil, tNumber())}}}}},
	})
	pair := serverWithWorker(t, "session:S1")
	if err := pair.server.Serve(counterV2, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "inc", methodFunc(func(any, Ctx) (any, error) { return float64(1), nil })), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, mustGet(t, pair.worker, "x")); !IsChordError(err, CodeContractMismatch) {
		t.Fatalf("host ready = %v", err)
	}
	down, up := MemoryLinkPair()
	if err := pair.server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: "client", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	if err := readyErr(t, mustGet(t, browser, "x")); !IsChordError(err, CodeUpgradeRequired) {
		t.Fatalf("browser ready = %v", err)
	}
}

// link.test.ts:310-323
func TestLinksInvalidatePrincipalRestampsTheLinkAndPropagates(t *testing.T) {
	c := cluster(t, LinkOptions{})
	browser := c.browser(map[string]any{"guest": "g1", "revision": float64(1)}, func(principal Principal) (Principal, bool) {
		next := map[string]any{}
		maps.Copy(next, principal.(map[string]any))
		next["revision"] = float64(2)
		return next, true
	})
	counter := mustGetSession(t, browser.host, "S1", "a")
	mustCall(t, counter, "add", float64(1), nil)
	if err := c.server.InvalidatePrincipal(PrincipalMatch{Pattern: chordjson.ObjectOf("guest", "g1")}); err != nil {
		t.Fatal(err)
	}
	// The relay under the old principal is closed at once; the worker disposes its object after.
	waitFor(t, "counter gone", func() bool { return counter.Status() == RefGone })
	waitFor(t, "close a", func() bool { return c.logged("close a") })
	again := mustGetSession(t, browser.host, "S1", "a")
	if got := jsonText(t, mustCall(t, again, "who", nil, nil)); got != `{"guest":"g1","revision":2}` {
		t.Fatalf("who = %s", got)
	}
}

// link.test.ts:325-341
func TestLinksAfterAReconnectSubscribersGetAResetAndCallsWork(t *testing.T) {
	c := cluster(t, LinkOptions{})
	browser := c.browser(map[string]any{"guest": "g1"}, nil)
	counter := mustGetSession(t, browser.host, "S1", "r")
	var mu sync.Mutex
	var events []string
	count := countReadable(t, counter)
	count.Events(func(event StateEvent[any]) {
		mu.Lock()
		events = append(events, string(event.Type))
		mu.Unlock()
	})
	mustCall(t, counter, "add", float64(2), nil)
	browser.up.Close()
	waitFor(t, "loading", func() bool { return counter.Status() == RefLoading })
	browser.reconnect()
	if got := mustCall(t, counter, "add", float64(1), nil); got != float64(1) {
		t.Fatalf("add after reconnect = %v", got)
	}
	// The old hold was released with the link, so the worker made a fresh object; the reset carries its value.
	waitFor(t, "n = 1", func() bool { return jsonText(t, count.Value()) == `{"n":1}` })
	mu.Lock()
	defer mu.Unlock()
	// The first hydration is the listener's snapshot; the re-hold's snapshot is a reset.
	if len(events) == 0 || events[0] != string(StateEventSnap) || !slices.Contains(events, string(StateEventReset)) {
		t.Fatalf("events = %v", events)
	}
}

// sendRecorder is a Link that records every frame sent through it (link.test.ts wraps up.send).
type sendRecorder struct {
	Link
	sent frameLog
}

func (recorder *sendRecorder) Send(frame Frame) (int, error) {
	recorder.sent.add(frame)
	return recorder.Link.Send(frame)
}

// link.test.ts "links: methods without arguments or result" "travel without args and without a value, and resolve to
// the result or undefined".
func TestLinksMethodsWithoutArgumentsOrResultTravelWithoutArgsAndWithoutAValueAndResolveToTheResultOrUndefined(t *testing.T) {
	tool := Remote("t.tool", RemoteDef{
		Version: 1,
		Input:   tObject(),
		Host:    NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{
			{"ping", Method(nil, nil)},
			{"count", Method(nil, tNumber())},
			{"note", Method(tString(), nil)},
		}}}},
	})
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	var mu sync.Mutex
	var notes []string
	if err := server.Serve(tool, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf(
			"variant", "main",
			"ping", methodFunc(func(any, Ctx) (any, error) { return nil, nil }),
			"count", methodFunc(func(any, Ctx) (any, error) { return float64(2), nil }),
			"note", methodFunc(func(text any, _ Ctx) (any, error) {
				mu.Lock()
				notes = append(notes, text.(string))
				mu.Unlock()
				return "dropped", nil
			}),
		), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	down, upLink := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	up := &sendRecorder{Link: upLink}
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: "client", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	ref, err := browser.Hold(tool, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if got := mustCall(t, ref, "ping", nil, nil); got != nil {
		t.Fatalf("ping = %v", got)
	}
	if got := mustCall(t, ref, "count", nil, nil); got != float64(2) {
		t.Fatalf("count = %v", got)
	}
	if got := mustCall(t, ref, "note", "hi", nil); got != nil {
		t.Fatalf("note = %v", got)
	}
	mu.Lock()
	if !slices.Equal(notes, []string{"hi"}) {
		t.Fatalf("notes = %v", notes)
	}
	mu.Unlock()
	var hasArgs []bool
	for _, frame := range up.sent.list() {
		if call, ok := frame.(CallFrame); ok {
			hasArgs = append(hasArgs, call.HasArgs)
		}
	}
	if !slices.Equal(hasArgs, []bool{false, false, true}) {
		t.Fatalf("calls carry args = %v", hasArgs)
	}
}

// protocolMismatches records what OnProtocolMismatch reports.
type protocolMismatches struct {
	mu   sync.Mutex
	seen []ProtocolVersions
}

func (mismatches *protocolMismatches) report(versions ProtocolVersions) {
	mismatches.mu.Lock()
	mismatches.seen = append(mismatches.seen, versions)
	mismatches.mu.Unlock()
}

func (mismatches *protocolMismatches) list() []ProtocolVersions {
	mismatches.mu.Lock()
	defer mismatches.mu.Unlock()
	return slices.Clone(mismatches.seen)
}

// wantMismatches fails unless exactly one mismatch was reported, with local ProtocolVersion and the given peer.
func wantMismatches(t *testing.T, mismatches *protocolMismatches, peer int) {
	t.Helper()
	waitFor(t, "a protocol mismatch", func() bool { return len(mismatches.list()) > 0 })
	seen := mismatches.list()
	if len(seen) != 1 || seen[0].Local != ProtocolVersion || seen[0].Peer == nil || *seen[0].Peer != peer {
		t.Fatalf("mismatches = %+v", seen)
	}
}

// link.test.ts "links: protocol version" "a peer without a protocol version (an old client) is told and dropped;
// nothing it sends is served".
func TestLinksProtocolVersionAPeerWithoutAProtocolVersionAnOldClientIsToldAndDroppedNothingItSendsIsServed(t *testing.T) {
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	serveCounter(t, server, nil)
	var accepted protocolMismatches
	down, raw := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}, LinkOptions: LinkOptions{OnProtocolMismatch: accepted.report}}); err != nil {
		t.Fatal(err)
	}
	var frames frameLog
	var mu sync.Mutex
	var closed *string
	raw.Receive(frames.add)
	raw.OnClose(func(reason string) {
		mu.Lock()
		closed = &reason
		mu.Unlock()
	})
	_, _ = raw.Send(HelloFrame{Name: "old", Kind: "client", CodeVersion: 0})
	_, _ = raw.Send(HoldFrame{ID: 1, At: "server", Token: linkCounter.Description, Input: map[string]any{"name": "c"}})
	wantMismatches(t, &accepted, 1)
	waitFor(t, "the close", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return closed != nil
	})
	mu.Lock()
	reason := *closed
	mu.Unlock()
	if want := fmt.Sprintf("protocol-version %d", ProtocolVersion); reason != want {
		t.Fatalf("close reason = %q, want %q", reason, want)
	}
	if !down.Closed() {
		t.Fatal("the server end of the link is open")
	}
	for _, frame := range frames.list() {
		if frame.T() == "shape" {
			t.Fatal("the old client's hold was served")
		}
	}
}

// link.test.ts "links: protocol version" "a client learns that its server speaks another version, and stops".
func TestLinksProtocolVersionAClientLearnsThatItsServerSpeaksAnotherVersionAndStops(t *testing.T) {
	var client protocolMismatches
	down, up := MemoryLinkPair()
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: "client", OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	browser.Connect(up, ConnectOptions{Principal: kindServer(), LinkOptions: LinkOptions{OnProtocolMismatch: client.report}})
	// A newer server: its hello announces the next version, and it closes the link the same way Chord does.
	_, _ = down.Send(HelloFrame{Protocol: ProtocolVersion + 1, Name: "server", Kind: "host", CodeVersion: 1})
	wantMismatches(t, &client, ProtocolVersion+1)
	waitFor(t, "the client to close the link", up.Closed)

	// Told by the peer (it noticed first): the close reason carries its version.
	var told protocolMismatches
	down2, up2 := MemoryLinkPair()
	other := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: "client", OnError: func(error) {}})
	t.Cleanup(func() { _ = other.Close() })
	other.Connect(up2, ConnectOptions{Principal: kindServer(), LinkOptions: LinkOptions{OnProtocolMismatch: told.report}})
	down2.Close(fmt.Sprintf("protocol-version %d", ProtocolVersion+1))
	wantMismatches(t, &told, ProtocolVersion+1)
}

// link.test.ts "links: protocol version" "peers of the same version talk; an ordinary close is no mismatch".
func TestLinksProtocolVersionPeersOfTheSameVersionTalkAnOrdinaryCloseIsNoMismatch(t *testing.T) {
	var both protocolMismatches
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}, LinkOptions: LinkOptions{OnProtocolMismatch: both.report}}); err != nil {
		t.Fatal(err)
	}
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: "client", OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	browser.Connect(up, ConnectOptions{Principal: kindServer(), LinkOptions: LinkOptions{OnProtocolMismatch: both.report}})
	time.Sleep(20 * time.Millisecond)
	up.Close("bye")
	waitFor(t, "the link to close", down.Closed)
	time.Sleep(20 * time.Millisecond)
	if seen := both.list(); len(seen) != 0 {
		t.Fatalf("mismatches = %+v", seen)
	}
}

// linkBig is link.test.ts (Pi 018c87dd) Big.
var linkBig = Remote("t.big", RemoteDef{
	Version: 1,
	Input:   tObject(),
	Host:    NewHostNamed("session:S1"),
	Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"notes", tArray(tString())}},
		Methods: Fields[MethodSpec]{{"echo", Method(tArray(tString()), tArray(tString()))}},
	}}},
})

// large is about 160,000 characters of JSON: above PackThreshold.
func large(tag string) []any {
	notes := make([]any, 40)
	for index := range notes {
		notes[index] = fmt.Sprintf("%s%d:%s", tag, index, strings.Repeat("x", 4000))
	}
	return notes
}

// link.test.ts (Pi 018c87dd) "links: packed payloads" "large args, results and snapshots cross a relay deflated and
// arrive intact".
func TestLinksPackedPayloadsLargeArgsResultsAndSnapshotsCrossARelayDeflatedAndArriveIntact(t *testing.T) {
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	server.Route("session:", Route{
		Principal: func(name string) Principal { return map[string]any{"host": name} },
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve: func(name string) (Link, error) {
			down, up := MemoryLinkPair()
			worker := CreateHost(HostOptions{Self: kindHost(), Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
			t.Cleanup(func() { _ = worker.Close() })
			err := worker.Serve(linkBig, func(_ any, _ Principal, self Self) (Implementation, error) {
				notes, err := self.State("notes", tArray(tString()), large("a"), nil)
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(
					"variant", "main",
					"notes", notes,
					"echo", methodFunc(func(notes any, _ Ctx) (any, error) { return notes, nil }),
				), nil
			}, ServeOptions{})
			if err != nil {
				return nil, err
			}
			return down, nil
		},
	})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"owner": true}}); err != nil {
		t.Fatal(err)
	}
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	big, err := browser.Hold(linkBig, map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := jsonText(t, mustCall(t, big, "echo", large("c"), nil)), jsonText(t, large("c")); got != want {
		t.Fatalf("echo returned %d characters, want the %d sent", len(got), len(want))
	}
	notes, ok := big.Member("notes").(Readable[any])
	if !ok {
		t.Fatal("notes is not a readable")
	}
	notes.Subscribe(func(any) {})
	want := jsonText(t, large("a"))
	waitFor(t, "the notes snapshot", func() bool { return notes.Value() != nil && jsonText(t, notes.Value()) == want })
}

// link.test.ts (Pi 018c87dd) "links: packed payloads" "only payloads above the threshold are packed; the receiver
// unpacks them".
func TestLinksPackedPayloadsOnlyPayloadsAboveTheThresholdArePackedTheReceiverUnpacksThem(t *testing.T) {
	a, b := MemoryLinkPair()
	var frames frameLog
	b.Receive(frames.add)
	for _, frame := range []Frame{
		ResultFrame{ID: 1, Value: large("v"), HasValue: true},
		ResultFrame{ID: 2, Value: []any{"small"}, HasValue: true},
		StateFrame{Hold: 3, M: "notes", As: "snap", Value: large("s"), HasValue: true},
	} {
		if _, err := a.Send(frame); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, "three frames", func() bool { return len(frames.list()) >= 3 })
	received := frames.list()
	unpacked := func(value any, want []any) packed {
		t.Helper()
		payload, ok := value.(packed)
		if !ok {
			t.Fatalf("payload %T is not packed", value)
		}
		got, err := unpack(payload)
		if err != nil {
			t.Fatal(err)
		}
		if jsonText(t, got) != jsonText(t, want) {
			t.Fatal("the unpacked payload differs from the one sent")
		}
		return payload
	}
	result, ok := received[0].(ResultFrame)
	if !ok {
		t.Fatalf("first frame %#v", received[0])
	}
	payload := unpacked(result.Value, large("v"))
	if small, ok := received[1].(ResultFrame); !ok || jsonText(t, small.Value) != `["small"]` {
		t.Fatalf("small frame %#v", received[1])
	}
	snap, ok := received[2].(StateFrame)
	if !ok {
		t.Fatalf("third frame %#v", received[2])
	}
	unpacked(snap.Value, large("s"))

	// A relay forwards the packed bytes as they are.
	c, d := MemoryLinkPair()
	var forwarded frameLog
	d.Receive(forwarded.add)
	result.ID = 9
	if _, err := c.Send(result); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the forwarded frame", func() bool { return len(forwarded.list()) >= 1 })
	relayed, ok := forwarded.list()[0].(ResultFrame)
	if !ok {
		t.Fatalf("forwarded %#v", forwarded.list()[0])
	}
	if bytesOf, ok := relayed.Value.(packed); !ok || !bytes.Equal(bytesOf.bytes, payload.bytes) {
		t.Fatalf("relayed payload %T is not the packed bytes received", relayed.Value)
	}
}

// epipeError is the `Object.assign(new Error("write EPIPE"), { code: "EPIPE" })` of link.test.ts.
type epipeError struct{}

func (epipeError) Error() string { return "write EPIPE" }
func (epipeError) Code() string  { return "EPIPE" }

// epipeIpcChannel is link.test.ts's IpcChannel whose every write fails asynchronously, as Node reports a write to a
// peer process that exited: to the send callback, or else as an `error` event nobody handles.
type epipeIpcChannel struct {
	mu   sync.Mutex
	sent []any
}

func (channel *epipeIpcChannel) Connected() bool { return true }

func (channel *epipeIpcChannel) Send(message any, callback func(err error)) bool {
	channel.mu.Lock()
	channel.sent = append(channel.sent, message)
	channel.mu.Unlock()
	if callback == nil {
		panic("an IPC send without a callback cannot report EPIPE")
	}
	go callback(epipeError{})
	return false
}

func (channel *epipeIpcChannel) On(string, *IpcListener)  {}
func (channel *epipeIpcChannel) Off(string, *IpcListener) {}

func (channel *epipeIpcChannel) sentCount() int {
	channel.mu.Lock()
	defer channel.mu.Unlock()
	return len(channel.sent)
}

// link.test.ts "links: IPC": a write that fails after the peer process exited (EPIPE) ends the link instead of raising
// an unhandled error.
func TestLinksIPCAWriteThatFailsAfterThePeerProcessExitedEPIPEEndsTheLinkInsteadOfRaisingAnUnhandledError(t *testing.T) {
	channel := &epipeIpcChannel{}
	link := NewIpcLink(channel)
	var reasons reasonLog
	link.OnClose(reasons.add)
	if _, err := link.Send(PingFrame{N: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the link to end", link.Closed)
	if got := reasons.list(); !slices.Equal(got, []string{"ipc send failed: EPIPE"}) {
		t.Fatalf("reasons = %q", got)
	}
	if channel.sentCount() != 1 {
		t.Fatalf("sent %d messages, want 1", channel.sentCount())
	}
	if _, err := link.Send(PingFrame{N: 2}); err != nil {
		t.Fatal(err)
	}
	if channel.sentCount() != 1 {
		t.Fatalf("a closed link sent %d messages, want 1", channel.sentCount())
	}
}
