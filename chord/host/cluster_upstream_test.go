package host

// pi: packages/chord/src/cluster.ts
// Ports packages/chord/test/cluster.test.ts ("cross-host reload"): a server and worker hosts over memory links,
// reloaded together. Pi's `await tick()` (setImmediate) lets one specific thing settle; each port waits for that
// condition (waitLinked, waitFor, a latch).

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// waitLinked waits for what Pi's first tick after setup() lets settle: both hellos are delivered, so the server runs
// the child in step (synced) and the child knows its parent.
func waitLinked(t *testing.T, server, child *HostImpl) {
	t.Helper()
	waitFor(t, child.Name()+" linked to "+server.Name(), func() bool {
		var synced, parented bool
		server.run(func() {
			connection := server.childNamed(child.Name())
			synced = connection != nil && connection.synced
		})
		child.run(func() { parented = child.upstream != nil && child.upstream.parentName != nil })
		return synced && parented
	})
}

// clusterLatch is cluster.test.ts latch().
type clusterLatch struct {
	once sync.Once
	done chan struct{}
}

func newClusterLatch() *clusterLatch { return &clusterLatch{done: make(chan struct{})} }
func (l *clusterLatch) open()        { l.once.Do(func() { close(l.done) }) }
func (l *clusterLatch) wait()        { <-l.done }

// waitT waits for the latch, failing the test after five seconds.
func (l *clusterLatch) waitT(t *testing.T, what string) {
	t.Helper()
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

var clusterCount = tObject("n", tNumber())

// clusterGreeter is cluster.test.ts Greeter.
var clusterGreeter = Remote("t.greeter", RemoteDef{
	Version: 1,
	Input:   tObject("session", tString()),
	Host:    NewHostFamily("session:", "session"),
	Variants: Fields[VariantDef]{
		{"main", VariantDef{
			State:   Fields[Schema]{{"count", clusterCount}},
			Methods: Fields[MethodSpec]{{"greet", Method(tString(), tString())}},
		}},
	},
})

// clusterGreeterV2 is cluster.test.ts GreeterV2.
var clusterGreeterV2 = Remote("t.greeter", RemoteDef{
	Version:  2,
	Input:    tObject("session", tString()),
	Host:     NewHostFamily("session:", "session"),
	Variants: Fields[VariantDef]{{"main", VariantDef{Methods: Fields[MethodSpec]{{"hello", Method(tString(), tString())}}}}},
})

// clusterServerGreeter is the server's own greeter: the greeter facet serves this one on the server, clusterGreeter on
// session hosts.
var clusterServerGreeter = Remote("t.server-greeter", RemoteDef{
	Version: 1,
	Input:   tObject(),
	Host:    NewHostNamed("server"),
	Variants: Fields[VariantDef]{
		{"main", VariantDef{
			State:   Fields[Schema]{{"count", clusterCount}},
			Methods: Fields[MethodSpec]{{"greet", Method(tString(), tString())}},
		}},
	},
})

func greeterImplementation(greeting string, stateVersion int, self Self) (Implementation, error) {
	count, err := self.State("count", clusterCount, delta.JsonObjectOf("n", float64(0)), &StateOptions{Version: stateVersion})
	if err != nil {
		return nil, err
	}
	return chordjson.ObjectOf(
		"variant", "main",
		"count", count,
		"greet", methodFunc(func(args any, ctx Ctx) (any, error) {
			if err := count.Change(ctx, func(draft any) {
				object := draft.(*delta.Object)
				_ = object.Set("n", object.Get("n").(float64)+1)
			}); err != nil {
				return nil, err
			}
			return greeting + " " + args.(string), nil
		}),
	), nil
}

// greeterFacet is cluster.test.ts greeterFacet(greeting, { fail }).
func greeterFacet(greeting string, fail bool) Facet {
	return greeterFacetVersion(greeting, fail, 1)
}

// greeterFacetVersion is greeterFacet with Pi's stateVersion option (default 1).
func greeterFacetVersion(greeting string, fail bool, stateVersion int) Facet {
	return Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		greeter := func(_ any, _ Principal, self Self) (Implementation, error) {
			if fail {
				return nil, errors.New("worker factory fails")
			}
			return greeterImplementation(greeting, stateVersion, self)
		}
		if api.Name == "server" {
			return api.Serve(clusterServerGreeter, greeter, ServeOptions{})
		}
		return api.Serve(clusterGreeter, greeter, ServeOptions{})
	}}}
}

// clusterModules is setup()'s modules map, read by the worker's loader.
type clusterModules struct {
	mu      sync.Mutex
	modules map[string]Facet
}

func (m *clusterModules) set(name string, facet Facet) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modules[name] = facet
}

func (m *clusterModules) loader(names []string) ([]Facet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	facets := make([]Facet, len(names))
	for index, name := range names {
		facets[index] = m.modules[name]
	}
	return facets, nil
}

type clusterSetup struct {
	server, worker *HostImpl
	modules        *clusterModules
	down           *MemoryLink
}

// setupCluster is cluster.test.ts setup(): server + worker over a memory link; the worker loads facets by name from
// modules.
func setupCluster(t *testing.T) clusterSetup {
	t.Helper()
	modules := &clusterModules{modules: map[string]Facet{"greeter": greeterFacet("v1", false)}}
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: chordjson.ObjectOf("worker", "w"), Kind: KindHost, Name: new("session:S1")}); err != nil {
		t.Fatal(err)
	}
	worker := CreateHost(HostOptions{Self: kindHost(), Name: "session:S1", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, Loader: modules.loader, OnError: func(error) {}})
	t.Cleanup(func() { _ = worker.Close(); _ = server.Close() })
	modules.mu.Lock()
	facet := modules.modules["greeter"]
	modules.mu.Unlock()
	if err := worker.Install(facet, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	return clusterSetup{server: server, worker: worker, modules: modules, down: down}
}

// getGreeter gets typ for session (empty: the empty input of a type placed on one host).
func getGreeter(t *testing.T, host *HostImpl, typ *Contract, session string) Ref {
	t.Helper()
	input := chordjson.ObjectOf()
	if session != "" {
		input = chordjson.ObjectOf("session", session)
	}
	ref, err := host.Hold(typ, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func greet(t *testing.T, ref Ref, name string, ctx Ctx) string {
	t.Helper()
	value, err := callMember(ref, "greet", name, ctx)
	if err != nil {
		t.Fatalf("greet %s: %v", name, err)
	}
	return value.(string)
}

func previousCodeVersion(host *HostImpl) any {
	if codeVersion, ok := host.PreviousCodeVersion(); ok {
		return codeVersion
	}
	return nil
}

func sessionRemote(names ...string) map[string][]string {
	return map[string][]string{"session:": names}
}

// cluster.test.ts:78-105 "prepares every host, commits the server first, then the workers; code versions stay in step".
func TestCrossHostReloadPreparesEveryHostCommitsTheServerFirstThenTheWorkersCodeVersionsStayInStep(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	greeter := getGreeter(t, s.server, clusterGreeter, "S1")
	if got := greet(t, greeter, "a", nil); got != "v1 a" {
		t.Fatalf("greet a = %q", got)
	}
	gate := newClusterLatch()
	type result struct {
		value any
		err   error
	}
	pinned := make(chan result, 1)
	entered := newClusterLatch()
	go func() {
		value, err := s.server.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(greeter, "greet", "p", ctx); err != nil {
				return nil, err
			}
			entered.open()
			gate.wait()
			return callMember(greeter, "greet", "p", ctx)
		}, RootOptions{})
		pinned <- result{value, err}
	}()
	// Pi's tick lets the pinned root make its first (old-code version) call.
	entered.waitT(t, "the pinned root's first call")
	var mu sync.Mutex
	var shapes []string
	greeter.OnShape(func(shape Shape) {
		mu.Lock()
		shapes = append(shapes, shape.Contract)
		mu.Unlock()
	})
	s.modules.set("greeter", greeterFacet("v2", false))
	report, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")})
	if err != nil {
		t.Fatal(err)
	}
	// No waiting: the commit frame precedes this call on the FIFO link, so the worker has committed when it arrives.
	if got := greet(t, greeter, "b", nil); got != "v2 b" {
		t.Fatalf("greet b = %q", got)
	}
	if s.server.CodeVersion() != report.CodeVersion || s.worker.CodeVersion() != report.CodeVersion {
		t.Fatalf("code versions server %d worker %d, report %d", s.server.CodeVersion(), s.worker.CodeVersion(), report.CodeVersion)
	}
	gate.open()
	if got := <-pinned; got.err != nil || got.value != "v1 p" {
		t.Fatalf("pinned = %v, %v", got.value, got.err)
	}
	mu.Lock()
	if len(shapes) != 1 {
		t.Fatalf("shapes = %v, want one shape push at the commit", shapes)
	}
	mu.Unlock()
	// The server retires the old code version once its last old root ended, then tells the worker.
	waitFor(t, "the worker to retire the old code version", func() bool { return previousCodeVersion(s.worker) == nil })
	if previousCodeVersion(s.server) != nil || previousCodeVersion(s.worker) != nil {
		t.Fatalf("previous code versions server %v worker %v", previousCodeVersion(s.server), previousCodeVersion(s.worker))
	}
}

// cluster.test.ts:107-121 "a failing vote aborts every host; the error keeps its code and names the host".
func TestCrossHostReloadAFailingVoteAbortsEveryHostTheErrorKeepsItsCodeAndNamesTheHost(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	greeter := getGreeter(t, s.server, clusterGreeter, "S1")
	greet(t, greeter, "a", nil)
	before := []int{s.server.CodeVersion(), s.worker.CodeVersion()}
	s.modules.set("greeter", greeterFacet("v2", true))
	_, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")})
	if !IsChordError(err, CodeUnavailable) || !regexp.MustCompile(`session:S1: .*worker factory fails`).MatchString(err.Error()) {
		t.Fatalf("err = %v", err)
	}
	if after := []int{s.server.CodeVersion(), s.worker.CodeVersion()}; !slices.Equal(after, before) {
		t.Fatalf("code versions %v, want %v", after, before)
	}
	if got := greet(t, greeter, "x", nil); got != "v1 x" {
		t.Fatalf("greet x = %q", got)
	}
}

// cluster.test.ts:123-152 "a worker gate blocked past its deadline aborts the whole reload".
func TestCrossHostReloadAWorkerGateBlockedPastItsDeadlineAbortsTheWholeReload(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	greeter := getGreeter(t, s.server, clusterGreeter, "S1")
	gate := newClusterLatch()
	blocker := make(chan error, 1)
	entered := newClusterLatch()
	go func() {
		_, err := s.server.Root(func(ctx Ctx) (any, error) {
			if _, err := callMember(greeter, "greet", "g", ctx); err != nil {
				return nil, err
			}
			entered.open()
			gate.wait()
			return nil, nil
		}, RootOptions{})
		blocker <- err
	}()
	// Pi's tick lets the blocking root make its call, so the worker holds an old-code version root.
	entered.waitT(t, "the blocking root's call")
	before := []int{s.server.CodeVersion(), s.worker.CodeVersion()}
	bumped := greeterFacetVersion("v2", false, 2)
	s.modules.set("greeter", bumped)
	_, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter"), GateDeadline: new(30 * time.Millisecond)})
	var chordError *ChordError
	if !errors.As(err, &chordError) || chordError.Code != CodeAborted || chordError.Reason != "gate_deadline" ||
		!regexp.MustCompile(`session:S1: .*blocking roots: server#\w+:\d+`).MatchString(err.Error()) {
		t.Fatalf("err = %v", err)
	}
	if after := []int{s.server.CodeVersion(), s.worker.CodeVersion()}; !slices.Equal(after, before) {
		t.Fatalf("code versions %v, want %v", after, before)
	}
	gate.open()
	if err := <-blocker; err != nil {
		t.Fatal(err)
	}
	if got := greet(t, greeter, "x", nil); got != "v1 x" {
		t.Fatalf("greet x = %q", got)
	}
}

// cluster.test.ts:154-205 "removes facets from server and workers, reconciles removal tombstones, and clears them on
// re-add".
func TestCrossHostReloadRemovesFacetsFromServerAndWorkersReconcilesRemovalTombstonesAndClearsThemOnReAdd(t *testing.T) {
	s := setupCluster(t)
	if err := s.server.Install(greeterFacet("v1", false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLinked(t, s.server, s.worker)
	local := getGreeter(t, s.server, clusterServerGreeter, "")
	child := getGreeter(t, s.server, clusterGreeter, "S1")
	if got := greet(t, local, "a", nil); got != "v1 a" {
		t.Fatalf("local greet = %q", got)
	}
	if got := greet(t, child, "a", nil); got != "v1 a" {
		t.Fatalf("child greet = %q", got)
	}
	if _, err := s.server.Reload(nil, ReloadOptions{Remove: []string{"greeter"}, RemoteRemove: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the child reference to go", func() bool { return child.Status() == RefGone })
	if local.Status() != RefGone || child.Status() != RefGone {
		t.Fatalf("statuses local %s child %s", local.Status(), child.Status())
	}

	// A child starting with the old set must remove it without trying to import its removed module.
	cold := CreateHost(HostOptions{Self: kindHost(),
		Name: "session:cold",
		Loader: func([]string) ([]Facet, error) {
			t.Error("Removed modules must not be imported")
			return nil, errors.New("Removed modules must not be imported")
		},
		OnError: func(error) {},
	})
	t.Cleanup(func() { _ = cold.Close() })
	if err := cold.Install(greeterFacet("v1", false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	old := getGreeter(t, cold, clusterGreeter, "cold")
	if err := readyErr(t, old); err != nil {
		t.Fatal(err)
	}
	down, up := MemoryLinkPair()
	if err := s.server.Accept(down, AcceptOptions{Principal: chordjson.ObjectOf("worker", "cold"), Kind: KindHost, Name: new("session:cold")}); err != nil {
		t.Fatal(err)
	}
	cold.Connect(up, ConnectOptions{Principal: kindServer()})
	waitFor(t, "the cold host's reference to go", func() bool { return old.Status() == RefGone })
	if old.Status() != RefGone {
		t.Fatalf("old status = %s", old.Status())
	}
	if cold.CodeVersion() != s.server.CodeVersion() {
		t.Fatalf("cold code version %d, server %d", cold.CodeVersion(), s.server.CodeVersion())
	}
	// Close this loader-less child before the addition; it tested only the removal reconciliation.
	if err := cold.Close(); err != nil {
		t.Fatal(err)
	}
	s.modules.set("greeter", greeterFacet("v2", false))
	v2 := greeterFacet("v2", false)
	if _, err := s.server.Reload([]PluginRef{&v2}, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	if got := greet(t, getGreeter(t, s.server, clusterGreeter, "S1"), "b", nil); got != "v2 b" {
		t.Fatalf("greet b = %q", got)
	}
	fresh := CreateHost(HostOptions{Self: kindHost(), Name: "session:fresh", Loader: s.modules.loader, OnError: func(error) {}})
	t.Cleanup(func() { _ = fresh.Close() })
	if err := fresh.Install(greeterFacet("v1", false), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	freshDown, freshUp := MemoryLinkPair()
	if err := s.server.Accept(freshDown, AcceptOptions{Principal: chordjson.ObjectOf("worker", "fresh"), Kind: KindHost, Name: new("session:fresh")}); err != nil {
		t.Fatal(err)
	}
	fresh.Connect(freshUp, ConnectOptions{Principal: kindServer()})
	if got := greet(t, getGreeter(t, s.server, clusterGreeter, "fresh"), "c", nil); got != "v2 c" {
		t.Fatalf("greet c = %q", got)
	}
}

// cluster.test.ts:207-211 "only the server coordinates reloads".
func TestCrossHostReloadOnlyTheServerCoordinatesReloads(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	if _, err := s.worker.Reload(nil, ReloadOptions{}); !IsChordError(err, CodeUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

// cluster.test.ts:213-238 "browsers are not participants: they see shape and reset pushes, or upgrade_required".
func TestCrossHostReloadBrowsersAreNotParticipantsTheySeeShapeAndResetPushesOrUpgradeRequired(t *testing.T) {
	s := setupCluster(t)
	workerLink := s.down
	// The route resolves to the already connected worker.
	s.server.Route("session:", Route{
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve:   func(string) (Link, error) { return workerLink, nil },
		Principal: func(name string) Principal { return map[string]any{"host": name} },
	})
	down, up := MemoryLinkPair()
	if err := s.server.Accept(down, AcceptOptions{Principal: chordjson.ObjectOf("guest", "g")}); err != nil {
		t.Fatal(err)
	}
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	viewer := getGreeter(t, browser, clusterGreeter, "S1")
	if got := greet(t, viewer, "a", nil); got != "v1 a" {
		t.Fatalf("greet a = %q", got)
	}
	var mu sync.Mutex
	var shapes []string
	viewer.OnShape(func(shape Shape) {
		mu.Lock()
		shapes = append(shapes, shape.Contract)
		mu.Unlock()
	})
	shapeCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(shapes)
	}
	s.modules.set("greeter", greeterFacet("v2", false))
	_, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the shape push", func() bool { return shapeCount() > 0 })
	mu.Lock()
	if len(shapes) != 1 {
		t.Fatalf("shapes = %v, want one shape push at the commit", shapes)
	}
	mu.Unlock()
	if got := greet(t, viewer, "b", nil); got != "v2 b" {
		t.Fatalf("greet b = %q", got)
	}

	s.modules.set("greeter", Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(clusterGreeterV2, func(any, Principal, Self) (Implementation, error) {
			return chordjson.ObjectOf("variant", "main", "hello", methodFunc(func(args any, _ Ctx) (any, error) {
				return "hi " + args.(string), nil
			})), nil
		}, ServeOptions{})
	}}})
	if _, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the viewer to go", func() bool { return viewer.Status() == RefGone })
	if viewer.Status() != RefGone || !IsChordError(viewer.Err(), CodeUpgradeRequired) {
		t.Fatalf("viewer %s, err %v", viewer.Status(), viewer.Err())
	}
}

// cluster.test.ts:240-277 "an abort while a worker is still preparing: a late open closes itself and the worker can
// reload again".
func TestCrossHostReloadAnAbortWhileAWorkerIsStillPreparingALateOpenClosesItselfAndTheWorkerCanReloadAgain(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	greeter := getGreeter(t, s.server, clusterGreeter, "S1")
	greet(t, greeter, "a", nil)
	var mu sync.Mutex
	var log []string
	push := func(entry string) {
		mu.Lock()
		log = append(log, entry)
		mu.Unlock()
	}
	logged := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(log)
	}
	gate := newClusterLatch()
	started, startedOnce := make(chan struct{}), sync.Once{}
	slow := Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(clusterGreeter, func(_ any, _ Principal, self Self) (Implementation, error) {
			if _, err := self.Resource("late", ResourceDeclaration[any]{
				Version: 1,
				Open: func(ResourceOpenOptions) (any, error) {
					startedOnce.Do(func() { close(started) })
					gate.wait()
					push("opened")
					return nil, nil
				},
				Close: func(any) error {
					push("closed")
					return nil
				},
			}); err != nil {
				return nil, err
			}
			count, err := self.State("count", clusterCount, delta.JsonObjectOf("n", float64(0)), nil)
			if err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(
				"variant", "main",
				"count", count,
				"greet", methodFunc(func(args any, _ Ctx) (any, error) { return "slow " + args.(string), nil }),
			), nil
		}, ServeOptions{})
	}}}
	s.modules.set("greeter", slow)
	codeVersion := s.worker.CodeVersion()
	// Pi's one thread runs the worker's whole microtask chain, up to its pending open, before the vote deadline's timer
	// callback. The server's loop, which runs that callback, is held until the worker's open began, so the deadline
	// cannot abort a worker that is slower to start than 50 ms: it aborts one that is still preparing.
	reloadBetweenPreparePasses = func(host *HostImpl) {
		if host == s.server {
			host.run(func() {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Error("the worker's open did not begin")
				}
			})
		}
	}
	t.Cleanup(func() { reloadBetweenPreparePasses = nil })
	if _, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter"), VoteDeadline: new(50 * time.Millisecond)}); !IsChordError(err, CodeAborted) {
		t.Fatalf("err = %v", err)
	}
	// The abort reaches the worker before the open completes, as Pi's promise-chain link delivers it before the test's
	// next step.
	waitFor(t, "the worker to handle the abort", func() bool {
		return reviewOnLoop(s.worker, func() bool {
			operation := s.worker.childReloads.operation
			return operation != nil && operation.cancelled
		})
	})
	gate.open()
	waitFor(t, "the late resource to close", func() bool { return slices.Contains(logged(), "closed") })
	if got := logged(); !slices.Equal(got, []string{"opened", "closed"}) {
		t.Fatalf("log = %v", got)
	}
	// Pi's tick loop returns once the worker's microtasks ran, so its aborted reload has unwound and freed the reload
	// slot. That unwind follows the late resource's close on the worker's own goroutine: wait for the slot.
	var unwound <-chan struct{}
	s.worker.run(func() { unwound = s.worker.whenReloadEnded() })
	<-unwound
	if s.worker.CodeVersion() != codeVersion {
		t.Fatalf("worker code version %d, want %d", s.worker.CodeVersion(), codeVersion)
	}
	s.modules.set("greeter", greeterFacet("v3", false))
	if _, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	if got := greet(t, greeter, "b", nil); got != "v3 b" {
		t.Fatalf("greet b = %q", got)
	}
}

// droppingLink is cluster.test.ts dropping(link, drop): link, except that the frames drop selects are never sent (a
// frame lost when its link dies).
type droppingLink struct {
	inner Link
	drop  func(frame Frame) bool
}

func (link *droppingLink) Closed() bool { return link.inner.Closed() }
func (link *droppingLink) Send(frame Frame) (int, error) {
	if link.drop(frame) {
		return 0, nil
	}
	return link.inner.Send(frame)
}
func (link *droppingLink) Receive(handler func(frame Frame))   { link.inner.Receive(handler) }
func (link *droppingLink) OnClose(handler func(reason string)) { link.inner.OnClose(handler) }
func (link *droppingLink) Close(reason ...string)              { link.inner.Close(reason...) }

// clusterPing is cluster.test.ts Ping.
var clusterPing = Remote("t.cluster-ping", RemoteDef{
	Host:    NewHostNamed("server"),
	Version: 1,
	Input:   tObject(),
	Variants: Fields[VariantDef]{
		{"main", VariantDef{Methods: Fields[MethodSpec]{{"ping", Method(tNull(), tString())}}}},
	},
})

// clusterPlugin is the `{ name: "server-plugin", setup: () => {} }` facets of cluster.test.ts.
func clusterPlugin(name string) Facet {
	return Facet{Name: name, FacetDefinition: FacetDefinition{Setup: func(FacetApi) error { return nil }}}
}

// clusterPluginLoader is cluster.test.ts's server loader `names.map((name) => ({ name, setup: () => {} }))`, after
// wait (nil: at once) returned.
func clusterPluginLoader(wait func()) FacetLoader {
	return func(names []string) ([]Facet, error) {
		if wait != nil {
			wait()
		}
		facets := make([]Facet, len(names))
		for index, name := range names {
			facets[index] = clusterPlugin(name)
		}
		return facets, nil
	}
}

// joinWorker connects a child host named session:S1 to server over a memory link, as the tests that build their own
// server do: server.accept(down, ...) and createHost({ name, upstream: up, ... }).
func joinWorker(t *testing.T, server *HostImpl, link func(down *MemoryLink) Link, options HostOptions) (*HostImpl, *MemoryLink) {
	t.Helper()
	down, up := MemoryLinkPair()
	var accepted Link = down
	if link != nil {
		accepted = link(down)
	}
	if err := server.Accept(accepted, AcceptOptions{Principal: chordjson.ObjectOf("worker", "w"), Kind: KindHost, Name: new("session:S1")}); err != nil {
		t.Fatal(err)
	}
	options.Self = kindHost()
	options.Name = "session:S1"
	options.Upstream = &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}
	options.OnError = func(error) {}
	worker := CreateHost(options)
	t.Cleanup(func() { _ = worker.Close() })
	return worker, up
}

func clusterReloadAsync(host *HostImpl, plugins []PluginRef, options ReloadOptions) <-chan reloadOutcome {
	return reloadAsync(func() (any, error) { return host.Reload(plugins, options) })
}

func hostReloading(host *HostImpl) (reloading bool) {
	host.run(func() { reloading = host.reloading })
	return reloading
}

// cluster.test.ts "a child that says hello while a reload runs is reconciled after it, never left in step with the
// old code" (Pi 00d8d049).
func TestCrossHostReloadAChildThatSaysHelloWhileAReloadRunsIsReconciledAfterItNeverLeftInStepWithTheOldCode(t *testing.T) {
	modules := &clusterModules{modules: map[string]Facet{"greeter": greeterFacet("v1", false)}}
	loading := newClusterLatch()
	t.Cleanup(loading.open)
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}, Loader: clusterPluginLoader(loading.wait)})
	t.Cleanup(func() { _ = server.Close() })
	reloading := clusterReloadAsync(server, []PluginRef{ModuleRef("server-plugin")}, ReloadOptions{Remote: sessionRemote("greeter")})
	waitFor(t, "the reload to load its modules", func() bool { return hostReloading(server) })
	// The worker connects while the reload loads its modules: it announces the server's current code version.
	worker, _ := joinWorker(t, server, nil, HostOptions{Loader: modules.loader})
	if err := worker.Install(modules.modules["greeter"], LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hello to wait for the reload", func() bool {
		var deferred int
		server.run(func() { deferred = server.deferredHellos.Len() })
		return deferred == 1
	})
	modules.set("greeter", greeterFacet("v2", false))
	loading.open()
	outcome := reloadAwait(t, reloading)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if children := outcome.value.(ReloadReport).Children; len(children) != 0 {
		t.Fatalf("children = %v, want it not to be a participant", children)
	}
	greeter := getGreeter(t, server, clusterGreeter, "S1")
	if got := greet(t, greeter, "a", nil); got != "v2 a" {
		t.Fatalf("greet a = %q", got)
	}
	if worker.CodeVersion() != server.CodeVersion() {
		t.Fatalf("code versions worker %d server %d", worker.CodeVersion(), server.CodeVersion())
	}
}

// cluster.test.ts "a child keeps the previous code version until its parent retired it (spec 12)" (Pi 00d8d049).
func TestCrossHostReloadAChildKeepsThePreviousCodeVersionUntilItsParentRetiredItSpec12(t *testing.T) {
	s := setupCluster(t)
	waitLinked(t, s.server, s.worker)
	greeter := getGreeter(t, s.server, clusterGreeter, "S1")
	if got := greet(t, greeter, "a", nil); got != "v1 a" {
		t.Fatalf("greet a = %q", got)
	}
	first := s.server.CodeVersion()
	entered, gate := newClusterLatch(), newClusterLatch()
	// A server root of the first code version that calls the worker only after the next two reloads started.
	pinned := reloadAsync(func() (any, error) {
		return s.server.Root(func(ctx Ctx) (any, error) {
			entered.open()
			gate.wait()
			return callMember(greeter, "greet", "p", ctx)
		}, RootOptions{})
	})
	entered.waitT(t, "the pinned root")
	s.modules.set("greeter", greeterFacet("v2", false))
	if _, err := s.server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the worker to commit", func() bool { return s.worker.CodeVersion() != first })
	if previous, ok := s.worker.PreviousCodeVersion(); !ok || previous != first {
		t.Fatalf("worker previous = %d %v, want %d", previous, ok, first)
	}
	second := clusterReloadAsync(s.server, nil, ReloadOptions{Remote: sessionRemote(), RetireDeadline: new(2 * time.Second)})
	waitFor(t, "the second reload to wait for retirement", func() bool { return hostReloading(s.server) })
	time.Sleep(100 * time.Millisecond)
	// The server still waits for its root, so the worker must not have retired that code version.
	if previous, ok := s.worker.PreviousCodeVersion(); !ok || previous != first {
		t.Fatalf("worker previous = %d %v, want %d", previous, ok, first)
	}
	gate.open()
	if outcome := reloadAwait(t, pinned); outcome.err != nil || outcome.value != "v1 p" {
		t.Fatalf("pinned = %v %v", outcome.value, outcome.err)
	}
	if outcome := reloadAwait(t, second); outcome.err != nil {
		t.Fatal(outcome.err)
	}
	if previous, ok := s.server.PreviousCodeVersion(); ok {
		t.Fatalf("server previous = %d, want none", previous)
	}
}

// cluster.test.ts "a child reconciled after its parent retired old code versions may retire its own previous one at
// once" (Pi 00d8d049).
func TestCrossHostReloadAChildReconciledAfterItsParentRetiredOldCodeVersionsMayRetireItsOwnPreviousOneAtOnce(t *testing.T) {
	modules := &clusterModules{modules: map[string]Facet{"greeter": greeterFacet("v1", false)}}
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	plugin := clusterPlugin("server-plugin")
	if err := server.Install(plugin, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := server.Reload([]PluginRef{&plugin}, ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if previous, ok := server.PreviousCodeVersion(); ok {
		t.Fatalf("server previous = %d, want none", previous)
	}
	worker, _ := joinWorker(t, server, nil, HostOptions{Loader: modules.loader})
	if err := worker.Install(modules.modules["greeter"], LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the worker to be reconciled", func() bool { return worker.CodeVersion() == server.CodeVersion() })
	// Without the parent's retire watermark it would keep code version 1 until the parent's next retirement.
	waitFor(t, "the worker to retire its previous code version", func() bool {
		_, ok := worker.PreviousCodeVersion()
		return !ok
	})
	// And the next cluster reload does not wait for a retirement the parent will never announce.
	started := time.Now()
	if _, err := server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter"), RetireDeadline: new(10 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("the reload took %v", elapsed)
	}
}

// cluster.test.ts "a reconciliation running when a reload starts syncs the child only after it, to the committed code
// version" (Pi 00d8d049).
func TestCrossHostReloadAReconciliationRunningWhenAReloadStartsSyncsTheChildOnlyAfterItToTheCommittedCodeVersion(t *testing.T) {
	modules := &clusterModules{modules: map[string]Facet{"greeter": greeterFacet("v1", false)}}
	serverLoading, workerLoading, workerLoads := newClusterLatch(), newClusterLatch(), newClusterLatch()
	t.Cleanup(serverLoading.open)
	t.Cleanup(workerLoading.open)
	var blockServer, blockWorker atomic.Bool
	blockWorker.Store(true)
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}, Loader: clusterPluginLoader(func() {
		if blockServer.Load() {
			serverLoading.wait()
		}
	})})
	t.Cleanup(func() { _ = server.Close() })
	if err := server.Install(clusterPlugin("server-plugin"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Reload([]PluginRef{ModuleRef("server-plugin")}, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	// The worker connects behind: its reconciliation to code version 2 waits in the worker's module loading.
	worker, _ := joinWorker(t, server, nil, HostOptions{Loader: func(names []string) ([]Facet, error) {
		workerLoads.open()
		if blockWorker.Load() {
			workerLoading.wait()
		}
		return modules.loader(names)
	}})
	if err := worker.Install(modules.modules["greeter"], LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	workerLoads.waitT(t, "the worker's reconciliation to load its modules")
	// A reload starts before the reconciliation committed; the worker is not one of its participants.
	blockServer.Store(true)
	modules.set("greeter", greeterFacet("v2", false))
	reloading := clusterReloadAsync(server, []PluginRef{ModuleRef("server-plugin")}, ReloadOptions{Remote: sessionRemote("greeter")})
	waitFor(t, "the reload to start", func() bool { return hostReloading(server) })
	blockWorker.Store(false)
	workerLoading.open()
	waitFor(t, "the worker to commit code version 2", func() bool { return worker.CodeVersion() == 2 })
	serverLoading.open()
	outcome := reloadAwait(t, reloading)
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	report := outcome.value.(ReloadReport)
	if len(report.Children) != 0 || report.CodeVersion != 3 {
		t.Fatalf("report children %v code version %d, want none and 3", report.Children, report.CodeVersion)
	}
	greeter := getGreeter(t, server, clusterGreeter, "S1")
	if got := greet(t, greeter, "a", nil); got != "v2 a" {
		t.Fatalf("greet a = %q", got)
	}
	if worker.CodeVersion() != server.CodeVersion() {
		t.Fatalf("code versions worker %d server %d", worker.CodeVersion(), server.CodeVersion())
	}
}

// cluster.test.ts "a child behind whose hello waits for a running reload is fenced at once: its calls up are admitted
// (spec 9)" (Pi 00d8d049).
func TestCrossHostReloadAChildBehindWhoseHelloWaitsForARunningReloadIsFencedAtOnceItsCallsUpAreAdmittedSpec9(t *testing.T) {
	pingFacet := Facet{Name: "ping", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(clusterPing, func(any, Principal, Self) (Implementation, error) {
			return chordjson.ObjectOf("variant", "main", "ping", methodFunc(func(any, Ctx) (any, error) { return "pong", nil })), nil
		}, ServeOptions{})
	}}}
	loading := newClusterLatch()
	t.Cleanup(loading.open)
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}, Loader: clusterPluginLoader(loading.wait)})
	t.Cleanup(func() { _ = server.Close() })
	if err := server.Install(pingFacet, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Reload([]PluginRef{&pingFacet}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if previous, ok := server.PreviousCodeVersion(); ok {
		t.Fatalf("previous = %d, want code version 1 retired", previous)
	}
	reloading := clusterReloadAsync(server, []PluginRef{ModuleRef("server-plugin")}, ReloadOptions{Remote: sessionRemote()})
	waitFor(t, "the reload to load its modules", func() bool { return hostReloading(server) })
	worker, _ := joinWorker(t, server, nil, HostOptions{})
	// While the server's reload still loads its modules, a worker root (code version 1) calls the server.
	answer, err := worker.Root(func(ctx Ctx) (any, error) {
		ping, err := ctx.Get(clusterPing, chordjson.ObjectOf(), nil)
		if err != nil {
			return nil, err
		}
		return callMember(ping, "ping", nil, ctx)
	}, RootOptions{})
	if err != nil || answer != "pong" {
		t.Fatalf("answer = %v, %v", answer, err)
	}
	loading.open()
	if outcome := reloadAwait(t, reloading); outcome.err != nil {
		t.Fatal(outcome.err)
	}
	waitFor(t, "the worker to catch up", func() bool { return worker.CodeVersion() == server.CodeVersion() })
}

// cluster.test.ts "a reconnecting child behind its parent, still retaining its previous code version, reconciles
// without waiting out the retire deadline" (Pi 00d8d049).
func TestCrossHostReloadAReconnectingChildBehindItsParentStillRetainingItsPreviousCodeVersionReconcilesWithoutWaitingOutTheRetireDeadline(t *testing.T) {
	modules := &clusterModules{modules: map[string]Facet{"greeter": greeterFacet("v1", false)}}
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	// The retire frame is lost: the worker keeps code version 1, not permitted to retire it.
	worker, up := joinWorker(t, server, func(down *MemoryLink) Link {
		return &droppingLink{inner: down, drop: func(frame Frame) bool { return frame.T() == "retire" }}
	}, HostOptions{Loader: modules.loader})
	if err := worker.Install(modules.modules["greeter"], LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	waitLinked(t, server, worker)
	modules.set("greeter", greeterFacet("v2", false))
	if _, err := server.Reload(nil, ReloadOptions{Remote: sessionRemote("greeter")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the worker to commit", func() bool { return worker.CodeVersion() == server.CodeVersion() })
	if previous, ok := worker.PreviousCodeVersion(); !ok || previous != 1 {
		t.Fatalf("worker previous = %d %v, want 1", previous, ok)
	}
	up.Close("gone")
	waitFor(t, "both hosts to lose the link", func() bool {
		var parentless, orphaned bool
		worker.run(func() { parentless = worker.upstream == nil })
		server.run(func() { orphaned = server.childNamed("session:S1") == nil })
		return parentless && orphaned
	})
	// The server moves on while the worker is away, then the worker reconnects behind it.
	modules.set("greeter", greeterFacet("v3", false))
	if _, err := server.Reload([]PluginRef{func() *Facet { plugin := clusterPlugin("server-plugin"); return &plugin }()}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	down2, up2 := MemoryLinkPair()
	if err := server.Accept(down2, AcceptOptions{Principal: chordjson.ObjectOf("worker", "w"), Kind: KindHost, Name: new("session:S1")}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	worker.Connect(up2, ConnectOptions{Principal: kindServer()})
	waitFor(t, "the worker to be reconciled", func() bool { return worker.CodeVersion() == server.CodeVersion() })
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("reconciled after %v", elapsed)
	}
}

// cluster.test.ts "forced retirement cancels a browser call relayed to a worker before the server closes the old
// code's resources" (Pi 00d8d049).
func TestCrossHostReloadForcedRetirementCancelsABrowserCallRelayedToAWorkerBeforeTheServerClosesTheOldCodesResources(t *testing.T) {
	var log reloadLog
	server := CreateHost(HostOptions{Self: kindHost(), Name: "server", OnError: func(error) {}})
	t.Cleanup(func() { _ = server.Close() })
	down, up := MemoryLinkPair()
	worker := CreateHost(HostOptions{Self: kindHost(), Name: "session:S1", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = worker.Close() })
	never, waiting := newClusterLatch(), newClusterLatch()
	t.Cleanup(never.open)
	slow := Remote("t.cluster-slow", RemoteDef{
		Version: 1,
		Input:   tObject(),
		Host:    NewHostNamed("session:S1"),
		Variants: Fields[VariantDef]{
			{"main", VariantDef{Methods: Fields[MethodSpec]{{"wait", Method(tNull(), tUnknown())}}}},
		},
	})
	if err := worker.Serve(slow, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "wait", methodFunc(func(_ any, ctx Ctx) (any, error) {
			context.AfterFunc(ctx.Signal(), func() { log.push("worker aborted") })
			waiting.open()
			never.wait()
			return nil, nil
		})), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	// Records the cancel the server sends down: the guarantee is that it leaves before the old resources close.
	watched := &droppingLink{inner: down, drop: func(frame Frame) bool {
		if frame.T() == "cancel" {
			log.push("cancel sent")
		}
		return false
	}}
	server.Route("session:", Route{
		Authorize: func(Principal, string) (bool, error) { return true, nil },
		Resolve:   func(string) (Link, error) { return watched, nil },
		Principal: func(name string) Principal { return map[string]any{"host": name} },
	})
	store := Local("t.cluster-store", LocalDef{Version: 1, Input: tObject()})
	storeFacet := func(version int) Facet {
		return Facet{Name: "store", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(store, func(_ any, _ Principal, self Self) (Implementation, error) {
				if _, err := self.Resource("conn", ResourceDeclaration[any]{
					Version: version,
					Open:    func(ResourceOpenOptions) (any, error) { return chordjson.ObjectOf(), nil },
					Close:   func(any) error { log.push(fmt.Sprintf("server closes %d", version)); return nil },
				}); err != nil {
					return nil, err
				}
				return chordjson.ObjectOf(), nil
			}, ServeOptions{Lifetime: LifetimeHost})
		}}}
	}
	if err := server.Install(storeFacet(1), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	browserDown, browserUp := MemoryLinkPair()
	if err := server.Accept(browserDown, AcceptOptions{Principal: chordjson.ObjectOf("guest", "g")}); err != nil {
		t.Fatal(err)
	}
	browser := CreateHost(HostOptions{Self: kindHost(), Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: browserUp, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(error) {}})
	t.Cleanup(func() { _ = browser.Close() })
	// The relayed call's root runs on the server's code version 1.
	remoteSlow := getGreeter(t, browser, slow, "")
	call := reloadAsync(func() (any, error) { return callMember(remoteSlow, "wait", nil, nil) })
	waiting.waitT(t, "the relayed call to reach the worker")
	for _, facet := range []Facet{storeFacet(2)} {
		if _, err := server.Reload([]PluginRef{&facet}, ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	third := storeFacet(3)
	if _, err := server.Reload([]PluginRef{&third}, ReloadOptions{RetireDeadline: new(20 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-call:
		if outcome.err == nil {
			t.Fatal("the browser's call resolved, want it cancelled with its root")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the browser's call is still pending, want it cancelled with its root")
	}
	waitFor(t, "the worker to see the abort", func() bool { return slices.Contains(log.items(), "worker aborted") })
	var ordered []string
	for _, entry := range log.items() {
		if entry == "cancel sent" || entry == "server closes 1" {
			ordered = append(ordered, entry)
		}
	}
	if !slices.Equal(ordered, []string{"cancel sent", "server closes 1"}) {
		t.Fatalf("log = %q", log.items())
	}
}
