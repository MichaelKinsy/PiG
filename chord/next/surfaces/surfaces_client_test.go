package surfaces_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	. "github.com/MichaelKinsy/PiG/chord/next/surfaces"
	nodesurfaces "github.com/MichaelKinsy/PiG/chord/nextnode/surfaces"
)

// pi: packages/chord/test/surfaces/client.test.ts. Tests of a client host with its plugins (slice 11 §2, §4, decisions "Slice 11,
// P4" to "P6", p3 step 0a), over an in-memory link to a server serving the scripts object: the initial scripts loaded with the
// core facets, a new set applied in place (only changed scripts fetched; a failed one keeps its old facet and is tried again by
// the next set that has it), the stylesheets loaded before the reload and settled after it, a new base or an outdated server
// ending the client.

var ctx = context.Background()

func localText(name string) *contracts.Contract {
	c, err := contracts.Local(name, contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("text", contracts.Str())}})
	if err != nil {
		panic(err)
	}
	return c
}

var (
	greeting = localText("test.greeting")
	echo     = localText("test.echo")
)

func serving(contract *contracts.Contract, text string) host.Facet {
	return host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
		return h.Serve(contract, func(*delta.JsonObject, host.Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
				"text": host.Expose(state.NewConstant(text)),
			}}}, nil
		}, host.ServeOptions{})
	})
}

// script is a registered script of plugin (digest digest) serving greeting with text; "broken" throws instead, and its first
// failing runs throw too.
func script(plugin, digest, text string, failing ...int) RegisteredScript {
	var fails atomic.Int32
	if len(failing) > 0 {
		fails.Store(int32(failing[0]))
	}
	return RegisteredScript{Plugin: plugin, Digest: digest, Run: func(require func(string) (any, error), module *Module) error {
		if text == "panic" {
			panic("plugin threw")
		}
		if text == "broken" || fails.Add(-1) >= 0 {
			return errors.New("broken plugin")
		}
		if _, err := require("@test/chord"); err != nil {
			return err
		}
		if text == "routinefail" {
			module.Exports = ModuleExports{Default: host.DefineFacet(func(h host.FacetHost, _ host.FacetSelf) error {
				return h.Serve(greeting, func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
					self.OnLive(func(host.Ctx) *host.Handled { return host.Threw(errors.New("the routine failed")) })
					return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{
						"text": host.Expose(state.NewConstant("live")),
					}}}, nil
				}, host.ServeOptions{})
			})}
			return nil
		}
		module.Exports = ModuleExports{Default: serving(greeting, text)}
		return nil
	}}
}

// setOf is the set of scripts under base, each with a stylesheet named after its digest.
func setOf(base string, scripts ...RegisteredScript) ScriptSet {
	set := ScriptSet{Base: base, Combined: "/combined.js"}
	for _, s := range scripts {
		css := fmt.Sprintf("/%s.%s.css", s.Plugin, s.Digest)
		set.Plugins = append(set.Plugins, ScriptEntry{Name: s.Plugin, Digest: s.Digest, URL: fmt.Sprintf("/%s.%s.js", s.Plugin, s.Digest), CSS: &css})
	}
	return set
}

// manualTimer is a timer of the client's environment the test fires itself: its pause and its callback.
type manualTimer struct {
	ms   int64
	fire func()
}

type timers struct {
	mu   sync.Mutex
	list []*manualTimer
}

func (t *timers) withMs(ms ...int64) []*manualTimer {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*manualTimer
	for _, each := range t.list {
		if slices.Contains(ms, each.ms) {
			out = append(out, each)
		}
	}
	return out
}

type socket struct {
	events SocketEvents
	link   link.Link
}

type clientSetup struct {
	server   host.Host
	client   *Client
	mu       sync.Mutex
	fetched  []string
	errs     []ClientError
	outdated int
	sockets  []socket
	base     string
}

func (s *clientSetup) fetchedNow() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.fetched)
}
func (s *clientSetup) errorKinds() []ClientErrorKind {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kinds []ClientErrorKind
	for _, e := range s.errs {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}
func (s *clientSetup) outdatedCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.outdated }
func (s *clientSetup) socketCount() int   { s.mu.Lock(); defer s.mu.Unlock(); return len(s.sockets) }
func (s *clientSetup) socket(i int) socket {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sockets[i]
}

type more struct {
	failing int
	manual  *timers
	styles  ClientStyles
	// onTimer is called with the pause of each timer the manual environment lists, before its After returns.
	onTimer func(ms int64)
	// fetching is called with each script entry the client fetches, before the fetch answers.
	fetching func(entry ScriptEntry)
}

// setup is a server serving WebScripts with initial, and a client started against it with available to fetch; failing fetches
// fail first (a network error), manual collects the environment's timers instead of running them.
func setup(t *testing.T, initial []RegisteredScript, available []RegisteredScript, opt more) *clientSetup {
	t.Helper()
	kind, err := contracts.NewHostKind("server")
	if err != nil {
		t.Fatal(err)
	}
	address := kind.Address(nil)
	server, err := host.CreateHost(host.HostOptions{Address: &address, Principal: delta.JsonObjectOf("kind", "server"), OnError: func(host.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	first := setOf("B1", initial...)
	if report, err := server.Load([]host.HostFacet{{Name: "chord.web", Facet: nodesurfaces.ScriptsFacet(nodesurfaces.Web, func() ScriptSet { return first })}}, ctx); err != nil || len(report.Failed) != 0 {
		t.Fatalf("server load: %v %v", err, report.Failed)
	}
	s := &clientSetup{server: server, base: "B1"}
	var failing atomic.Int32
	failing.Store(int32(opt.failing))
	env := LoopEnvironment{
		Now: func() int64 { return time.Now().UnixMilli() },
		After: func(ms int64, callback func()) func() {
			if opt.manual != nil {
				timer := &manualTimer{ms: ms, fire: callback}
				opt.manual.mu.Lock()
				opt.manual.list = append(opt.manual.list, timer)
				opt.manual.mu.Unlock()
				if opt.onTimer != nil {
					opt.onTimer(ms)
				}
				return func() {
					opt.manual.mu.Lock()
					defer opt.manual.mu.Unlock()
					if at := slices.Index(opt.manual.list, timer); at >= 0 {
						opt.manual.list = slices.Delete(opt.manual.list, at, at+1)
					}
				}
			}
			timer := time.AfterFunc(time.Duration(ms)*time.Millisecond, callback)
			return func() { timer.Stop() }
		},
		Online: func() bool { return true },
		OnWake: func(func()) func() { return func() {} },
	}
	client, err := StartClient(ClientOptions{
		Scripts: ScriptsContract(WebScripts),
		Base:    "B1",
		Initial: initial,
		Modules: ClientModules{"@test/chord": struct{}{}},
		Core:    []host.HostFacet{{Name: "@test/core", Facet: serving(echo, "core")}},
		Open: func(events SocketEvents) link.Link {
			mine, theirs := link.MemoryLinkPair()
			if err := server.Accept(theirs, host.LinkOptions{Principal: delta.JsonObjectOf("kind", "user", "id", "u")}, ctx); err != nil {
				t.Error(err)
			}
			s.mu.Lock()
			s.sockets = append(s.sockets, socket{events, mine})
			s.mu.Unlock()
			// As Pi's setup queues the link's `opened()` as a microtask, which runs before the test body: the loop's outbox delivers
			// this once connect returns, so the client's Connected state lands before StartClient does. A goroutine has no such
			// order: a Connected landing after the test published makes `reconnected` apply the newest set again, so a set whose
			// script failed to load is fetched a second time.
			events.Opened()
			return mine
		},
		FetchScript: func(_ context.Context, entry ScriptEntry) (RegisteredScript, error) {
			s.mu.Lock()
			s.fetched = append(s.fetched, entry.URL)
			s.mu.Unlock()
			if opt.fetching != nil {
				opt.fetching(entry)
			}
			if failing.Add(-1) >= 0 {
				return RegisteredScript{}, fmt.Errorf("%s: network error", entry.URL)
			}
			for _, each := range available {
				if each.Plugin == entry.Name && each.Digest == entry.Digest {
					return each, nil
				}
			}
			return RegisteredScript{}, fmt.Errorf("no %s", entry.URL)
		},
		ServerBase: func(context.Context) (*string, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			base := s.base
			return &base, nil
		},
		Outdated:    func() { s.mu.Lock(); s.outdated++; s.mu.Unlock() },
		Styles:      opt.styles,
		Environment: env,
		OnError:     func(e ClientError) { s.mu.Lock(); s.errs = append(s.errs, e); s.mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	s.client = client
	return s
}

func (s *clientSetup) finish(t *testing.T) {
	t.Helper()
	if err := s.client.Stop(ctx); err != nil {
		t.Errorf("client stop: %v", err)
	}
	if err := s.server.Close(ctx); err != nil {
		t.Errorf("server close: %v", err)
	}
	noClientGoroutines(t)
}

// noClientGoroutines waits until no goroutine of the surfaces package is left: the update worker and the retry questions end with
// their client.
func noClientGoroutines(t *testing.T) {
	t.Helper()
	for i := 0; ; i++ {
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		var left []string
		for stack := range strings.SplitSeq(string(buf), "\n\n") {
			if strings.Contains(stack, "chord/next/surfaces.") && !strings.Contains(stack, "testing.tRunner") && !strings.Contains(stack, "runtime.Stack") {
				left = append(left, stack)
			}
		}
		if len(left) == 0 {
			return
		}
		if i > 2000 {
			t.Fatalf("goroutines of the package outlive their client:\n%s", strings.Join(left, "\n\n"))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *clientSetup) publish(t *testing.T, set ScriptSet) {
	t.Helper()
	if err := nodesurfaces.PublishTo(s.server, nodesurfaces.Web, set, ctx); err != nil {
		t.Fatal(err)
	}
}

// text is the text of a greeting or echo reference.
func text(t *testing.T, h host.Host, contract *contracts.Contract) *host.Reference {
	t.Helper()
	reference, err := h.Get(ctx, contract, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func valueOf(r *host.Reference) string {
	v, err := r.Member("text").Value()
	if err != nil {
		return "error: " + err.Error()
	}
	s, _ := v.(string)
	return s
}

// until waits until check holds.
func until(t *testing.T, check func() bool) {
	t.Helper()
	for i := 0; i < 2000 && !check(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if !check() {
		t.Fatal("condition did not hold in 10 s")
	}
}

func TestLoadsTheInitialScriptsWithTheCoreFacets(t *testing.T) {
	s := setup(t, []RegisteredScript{script("@test/a", "d1", "hello")}, nil, more{})
	if got := valueOf(text(t, s.client.Host, greeting)); got != "hello" {
		t.Fatalf("greeting = %q", got)
	}
	if got := valueOf(text(t, s.client.Host, echo)); got != "core" {
		t.Fatalf("echo = %q", got)
	}
	if f := s.fetchedNow(); len(f) != 0 {
		t.Fatalf("fetched = %v", f)
	}
	s.finish(t)
}

// client.ts startClient: a plugin whose initial script fails is reported once at the start (a plugins error naming it, with the error),
// the client starts with the others (Pi: the first `load`'s failed list goes to `onError`).
func TestStartClientReportsAnInitialScriptThatFailsAndStartsWithTheOthers(t *testing.T) {
	good, broken := script("@test/b", "d1", "fine"), script("@test/a", "d1", "broken")
	s := setup(t, []RegisteredScript{good, broken}, nil, more{})
	kinds := s.errorKinds()
	s.mu.Lock()
	var plugins ClientError
	for _, e := range s.errs {
		if e.Kind == ClientPluginsError {
			plugins = e
		}
	}
	s.mu.Unlock()
	if !slices.Equal(kinds, []ClientErrorKind{ClientPluginsError}) || len(plugins.Failed) != 1 || plugins.Failed[0].Plugin != "@test/a" || plugins.Failed[0].Error == nil {
		t.Fatalf("errors = %v, failed = %+v; want one plugins error naming @test/a", kinds, plugins.Failed)
	}
	if got := valueOf(text(t, s.client.Host, greeting)); got != "fine" {
		t.Fatalf("greeting = %q, want the good plugin's", got)
	}
	s.finish(t)
}

// A failure of the client's own host (a plugin's onLive routine that throws) reaches onError as a host error (Pi: `onError({ kind: "host", error })` of the host's `onError`).
func TestStartClientReportsAHostFailureAsAHostError(t *testing.T) {
	s := setup(t, []RegisteredScript{script("@test/a", "d1", "routinefail")}, nil, more{})
	reference := text(t, s.client.Host, greeting)
	defer reference.Release()
	until(t, func() bool { return slices.Contains(s.errorKinds(), ClientHostError) })
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.errs {
		if e.Kind == ClientHostError && e.Host.Kind == host.ErrRoutineFailed {
			return
		}
	}
	t.Fatalf("no routine_failed host error among %+v", s.errs)
}

func TestFetchesOnlyChangedScriptsReloadsTheHostKeepsAnUnchangedPluginsFacet(t *testing.T) {
	a1, a2 := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again")
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{a2}, more{})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", a2))
	until(t, func() bool { return valueOf(g) == "hello again" })
	if f := s.fetchedNow(); !slices.Equal(f, []string{"/@test/a.d2.js"}) {
		t.Fatalf("fetched = %v", f)
	}
	s.finish(t)
}

func TestKeepsAPluginsOldFacetWhenItsNewScriptFailsAndReportsIt(t *testing.T) {
	a1, broken := script("@test/a", "d1", "hello"), script("@test/a", "d2", "broken")
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{broken}, more{})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", broken))
	until(t, func() bool { return len(s.errorKinds()) > 0 })
	s.mu.Lock()
	first := s.errs[0]
	s.mu.Unlock()
	if first.Kind != ClientPluginsError || len(first.Failed) != 1 || first.Failed[0].Plugin != "@test/a" {
		t.Fatalf("error = %+v", first)
	}
	if got := valueOf(g); got != "hello" {
		t.Fatalf("greeting = %q", got)
	}
	s.finish(t)
}

// A script that panics, or has no function, is what a script that throws is in Pi (client.ts `entryOf.prepare` catches what
// evaluateRegistered throws): the plugin is reported failed and keeps its old facet; the client lives on.
func TestKeepsAPluginsOldFacetWhenItsNewScriptPanicsOrHasNoFunctionAndReportsIt(t *testing.T) {
	a1 := script("@test/a", "d1", "hello")
	for name, broken := range map[string]RegisteredScript{
		"panics":      script("@test/a", "d2", "panic"),
		"no function": {Plugin: "@test/a", Digest: "d2"},
	} {
		t.Run(name, func(t *testing.T) {
			s := setup(t, []RegisteredScript{a1}, []RegisteredScript{broken}, more{})
			g := text(t, s.client.Host, greeting)
			s.publish(t, setOf("B1", broken))
			until(t, func() bool { return len(s.errorKinds()) > 0 })
			s.mu.Lock()
			first := s.errs[0]
			s.mu.Unlock()
			if first.Kind != ClientPluginsError || len(first.Failed) != 1 || first.Failed[0].Plugin != "@test/a" || first.Failed[0].Error == nil {
				t.Fatalf("error = %+v", first)
			}
			if got := valueOf(g); got != "hello" {
				t.Fatalf("greeting = %q", got)
			}
			s.finish(t)
		})
	}
}

func TestTriesAFailedPluginsScriptAgainWhenAReconnectsSnapshotHasItNotTakenAsApplied(t *testing.T) {
	a1, flaky := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again", 1)
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{flaky}, more{})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", flaky))
	until(t, func() bool { return slices.Contains(s.errorKinds(), ClientPluginsError) })
	if got := valueOf(g); got != "hello" {
		t.Fatalf("greeting = %q", got)
	}
	until(t, func() bool { return s.socketCount() >= 1 })
	s.socket(0).link.Close()
	until(t, func() bool { return valueOf(g) == "hello again" })
	if f := s.fetchedNow(); !slices.Equal(f, []string{"/@test/a.d2.js", "/@test/a.d2.js"}) {
		t.Fatalf("fetched = %v", f)
	}
	s.finish(t)
}

// recording is a ClientStyles writing what it is asked into calls, its loads finishing when the test says.
type recording struct {
	mu     sync.Mutex
	calls  []string
	loaded chan struct{}
}

func (r *recording) Prepare(c context.Context, set ScriptSet) {
	var css []string
	for _, p := range set.Plugins {
		css = append(css, *p.CSS)
	}
	r.mu.Lock()
	r.calls = append(r.calls, "prepare "+strings.Join(css, " "))
	r.mu.Unlock()
	select {
	case <-r.loaded:
	case <-c.Done():
	}
}

func (r *recording) Settle(set ScriptSet, kept map[string]bool) {
	var names []string
	for n := range kept {
		names = append(names, n)
	}
	slices.Sort(names)
	r.mu.Lock()
	r.calls = append(r.calls, fmt.Sprintf("settle %d kept [%s]", len(set.Plugins), strings.Join(names, " ")))
	r.mu.Unlock()
}

func (r *recording) count() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

func TestLoadsTheNewStylesheetsBeforeTheReloadThenSettlesThemWithThePluginsThatKeptTheirOldCode(t *testing.T) {
	a1, a2, broken := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again"), script("@test/b", "e2", "broken")
	r := &recording{loaded: make(chan struct{})}
	s := setup(t, []RegisteredScript{a1, script("@test/b", "e1", "b")}, []RegisteredScript{a2, broken}, more{styles: r})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", a2, broken))
	until(t, func() bool { return r.count() == 1 })
	// The new code waits for its stylesheets: the reload is not under way while Prepare has not returned.
	if got := valueOf(g); got != "hello" {
		t.Fatalf("greeting = %q before the stylesheets loaded", got)
	}
	close(r.loaded)
	until(t, func() bool { return r.count() == 2 })
	r.mu.Lock()
	calls := slices.Clone(r.calls)
	r.mu.Unlock()
	if want := []string{"prepare /@test/a.d2.css /@test/b.e2.css", "settle 2 kept [@test/b]"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
	s.finish(t)
}

func TestEndsTheWaitForStylesheetsAtTheClientsStopTheUpdateStopsThere(t *testing.T) {
	a1, a2 := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again")
	r := &recording{loaded: make(chan struct{})}
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{a2}, more{styles: r})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", a2))
	until(t, func() bool { return r.count() == 1 })
	if got := valueOf(g); got != "hello" {
		t.Fatalf("greeting = %q", got)
	}
	s.finish(t)
	// The update ended at the stop: no settle follows.
	time.Sleep(50 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if want := []string{"prepare /@test/a.d2.css"}; !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q, want %q", r.calls, want)
	}
}

func TestTriesASetWhoseScriptDidNotLoadAgainAfter1sThen2sUntilItApplies(t *testing.T) {
	a1, a2 := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again")
	manual := &timers{}
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{a2}, more{failing: 2, manual: manual})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", a2))
	until(t, func() bool { return len(manual.withMs(1000, 2000)) == 1 })
	if kinds := s.errorKinds(); !slices.Equal(kinds, []ClientErrorKind{ClientUpdateFailed}) {
		t.Fatalf("errors = %v", kinds)
	}
	if ms := manual.withMs(1000, 2000)[0].ms; ms != 1000 {
		t.Fatalf("first retry = %d ms", ms)
	}
	manual.withMs(1000, 2000)[0].fire()
	until(t, func() bool { return len(manual.withMs(2000)) == 1 })
	if kinds := s.errorKinds(); !slices.Equal(kinds, []ClientErrorKind{ClientUpdateFailed, ClientUpdateFailed}) {
		t.Fatalf("errors = %v", kinds)
	}
	manual.withMs(2000)[0].fire()
	until(t, func() bool { return valueOf(g) == "hello again" })
	if f := s.fetchedNow(); !slices.Equal(f, []string{"/@test/a.d2.js", "/@test/a.d2.js", "/@test/a.d2.js"}) {
		t.Fatalf("fetched = %v", f)
	}
	s.finish(t)
}

func TestDropsTheRetryOfASetANewerOneReplacedAndAppliesTheNewerOne(t *testing.T) {
	a1, a2, a3 := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again"), script("@test/a", "d3", "hello at last")
	manual := &timers{}
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{a2, a3}, more{failing: 1, manual: manual})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", a2))
	until(t, func() bool { return len(manual.withMs(1000)) == 1 })
	s.publish(t, setOf("B1", a3))
	until(t, func() bool { return valueOf(g) == "hello at last" })
	// The retry was cancelled with the newer set: nothing is left to fire.
	if n := len(manual.withMs(1000)); n != 0 {
		t.Fatalf("%d retries left", n)
	}
	if f := s.fetchedNow(); !slices.Equal(f, []string{"/@test/a.d2.js", "/@test/a.d3.js"}) {
		t.Fatalf("fetched = %v", f)
	}
	s.finish(t)
}

// A retry is scheduled in two steps, its timer and then its handle, so a newer set that lands between them has no handle to
// cancel the retry with (Pi's single thread has no between: its `#retry = { cancel: after(…) }` is one step). The retry must
// still go with the newer set: a newer one replaced it. The update that arms it here is a retry's own (the first retry's timer
// fired): the watch's call for a set returns when its update ended, so only an update the watch did not start (a retry's, a
// reconnect's) runs while the next set is delivered.
func TestDropsTheRetryOfASetANewerOneReplacedWhileItsTimerWasBeingSet(t *testing.T) {
	a1, a2, a3 := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again"), script("@test/a", "d3", "hello at last")
	manual := &timers{}
	setting, resume := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	defer release()
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{a2, a3}, more{failing: 2, manual: manual, onTimer: func(ms int64) {
		if ms == 2000 {
			setting <- struct{}{}
			<-resume
		}
	}})
	g := text(t, s.client.Host, greeting)
	s.publish(t, setOf("B1", a2))
	until(t, func() bool { return len(manual.withMs(1000)) == 1 })
	// The first retry runs on the client's own update goroutine, not in the watch's call: it fails again and arms the second.
	manual.withMs(1000)[0].fire()
	// The second retry's timer is listed and its After has not returned: the client holds no handle for it yet.
	select {
	case <-setting:
	case <-time.After(10 * time.Second):
		t.Fatal("no second retry timer in 10 s")
	}
	s.publish(t, setOf("B1", a3))
	// The newer set has reached the client's watch while the retry's After has not returned.
	until(t, func() bool { return s.client.NewestDigest("@test/a") == "d3" })
	release()
	until(t, func() bool { return valueOf(g) == "hello at last" })
	// The first retry's timer fired and stays listed; the second was cancelled with the newer set.
	if n := len(manual.withMs(2000)); n != 0 {
		t.Fatalf("%d second retries left", n)
	}
	if f := s.fetchedNow(); !slices.Equal(f, []string{"/@test/a.d2.js", "/@test/a.d2.js", "/@test/a.d3.js"}) {
		t.Fatalf("fetched = %v", f)
	}
	s.finish(t)
}

func TestCallsOutdatedForASetWithAnotherBase(t *testing.T) {
	s := setup(t, nil, nil, more{})
	s.publish(t, setOf("B2"))
	until(t, func() bool { return s.outdatedCount() == 1 })
	s.finish(t)
}

func TestAsksTheServersBaseBeforeReconnectingAnotherOneCallsOutdatedAndStops(t *testing.T) {
	s := setup(t, nil, nil, more{})
	s.mu.Lock()
	s.base = "B2"
	s.mu.Unlock()
	until(t, func() bool { return s.socketCount() >= 1 })
	s.socket(0).link.Close()
	until(t, func() bool { return s.outdatedCount() == 1 })
	time.Sleep(400 * time.Millisecond)
	if n := s.socketCount(); n != 1 {
		t.Fatalf("sockets = %d, want 1", n)
	}
	s.finish(t)
}

// Pi's watch callback is `async (call, ctx) => { ...; await this.#queue(set, ctx); }` and the watch delivers one call at a time: a set
// the scripts object shows while an update runs is not delivered (not `#latest`, no retry cancelled, no pause reset) until that update
// ended. So a retry armed by the update that ran meanwhile is cancelled by the next delivery, and the next set's own failure waits
// the first pause again, not a doubled one.
func TestANewerSetIsNotDeliveredWhileTheUpdateBeforeItRunsSoItsRetryWaitsTheFirstPauseAgain(t *testing.T) {
	a1, a2, a3 := script("@test/a", "d1", "hello"), script("@test/a", "d2", "hello again"), script("@test/a", "d3", "hello at last")
	manual := &timers{}
	blocked, resume := make(chan struct{}), make(chan struct{})
	var once, closing sync.Once
	release := func() { closing.Do(func() { close(resume) }) }
	defer release()
	s := setup(t, []RegisteredScript{a1}, []RegisteredScript{a2, a3}, more{failing: 2, manual: manual, fetching: func(entry ScriptEntry) {
		if entry.Digest == "d2" {
			once.Do(func() { close(blocked); <-resume })
		}
	}})
	s.publish(t, setOf("B1", a2))
	select {
	case <-blocked:
	case <-time.After(10 * time.Second):
		t.Fatal("the first update's fetch did not start in 10 s")
	}
	s.publish(t, setOf("B1", a3))
	// The first update is still running: the newer set waits for the watch's one call to return.
	for deadline := time.Now().Add(300 * time.Millisecond); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if digest := s.client.NewestDigest("@test/a"); digest != "d2" {
			t.Fatalf("the newest set is %q while the update of d2 runs, want d2 (the watch's call has not returned)", digest)
		}
	}
	release()
	until(t, func() bool { return slices.Contains(s.fetchedNow(), "/@test/a.d3.js") && len(s.errorKinds()) == 2 })
	until(t, func() bool { return len(manual.withMs(1000, 2000)) == 1 })
	if ms := manual.withMs(1000, 2000)[0].ms; ms != 1000 {
		t.Fatalf("the newer set's retry waits %d ms, want 1000 (its delivery reset the pause)", ms)
	}
	s.finish(t)
}
