package host

// Ports packages/chord/test/requests.test.ts: regression tests for resolved application requests #1-#10, numbered as
// in their Pi titles. Pi's awaits are blocking calls off the host loop; Pi's tick/until waits are waitFor on the
// condition the next assertion reads.

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

var requestsView = tObject("text", tString())

// requestsHost is createHost with a test cleanup.
func requestsHost(t *testing.T, options HostOptions) *HostImpl {
	t.Helper()
	if options.OnError == nil {
		options.OnError = func(error) {}
	}
	if options.Self == nil {
		options.Self = kindHost()
	}
	host := CreateHost(options)
	t.Cleanup(func() { _ = host.Close() })
	return host
}

func requestsGet(t *testing.T, host *HostImpl, typ *Contract, input any, options *GetOptions) Ref {
	t.Helper()
	ref, err := host.Hold(typ, input, options)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func requestsReadable(t *testing.T, ref Ref, name string) Readable[any] {
	t.Helper()
	readable, ok := ref.Member(name).(Readable[any])
	if !ok {
		t.Fatalf("%s is not state", name)
	}
	return readable
}

func requestsEmpty() *chordjson.Object { return chordjson.NewObject(0) }

// requestsLatch is Pi's latch(): a promise opened once.
type requestsLatch struct {
	once sync.Once
	ch   chan struct{}
}

func newRequestsLatch() *requestsLatch { return &requestsLatch{ch: make(chan struct{})} }
func (latch *requestsLatch) open()     { latch.once.Do(func() { close(latch.ch) }) }
func (latch *requestsLatch) wait()     { <-latch.ch }

func requestsServe(t *testing.T, serve func(*Contract, Factory, ServeOptions) error, typ *Contract, factory Factory, options ServeOptions) {
	t.Helper()
	if err := serve(typ, factory, options); err != nil {
		t.Fatal(err)
	}
}

func requestsChordCode(err error) ChordErrorCode {
	if chordError, ok := errors.AsType[*ChordError](err); ok {
		return chordError.Code
	}
	return ""
}

// requests.test.ts:43-91 "#1 a held object's state member can be returned as this object's state member"
func TestRequests1AHeldObjectsStateMemberCanBeReturnedAsThisObjectsStateMember(t *testing.T) {
	conv := Remote("t.conv", RemoteDef{
		Version: 1,
		Input:   tObject(), Host: NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"main", VariantDef{
			State:   Fields[Schema]{{"view", requestsView}},
			Methods: Fields[MethodSpec]{{"set", Method(tString(), nil)}},
		}}},
	})
	chat := Remote("t.chat", RemoteDef{
		Version: 1,
		Input:   tObject(), Host: NewHostNamed("server"),
		Variants: Fields[VariantDef]{{"viewer", VariantDef{State: Fields[Schema]{{"view", requestsView}}}}},
	})
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, conv, func(_ any, _ Principal, self Self) (Implementation, error) {
		view, err := self.State("view", requestsView, delta.JsonObjectOf("text", ""), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"variant", "main",
			"view", view,
			"set", methodFunc(func(text any, ctx Ctx) (any, error) {
				return nil, view.Replace(ctx, delta.JsonObjectOf("text", text), CellReplaceOptions{})
			}),
		), nil
	}, ServeOptions{})
	// No derivation, no cast: the reference member is the implementation member.
	requestsServe(t, server.Serve, chat, func(_ any, _ Principal, self Self) (Implementation, error) {
		held, err := self.Get(conv, requestsEmpty(), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "viewer", "view", held.Member("view")), nil
	}, ServeOptions{})
	chatRef := requestsGet(t, server, chat, requestsEmpty(), nil)
	view := requestsReadable(t, chatRef, "view")
	var mu sync.Mutex
	var values []string
	view.Subscribe(func(value any) {
		mu.Lock()
		values = append(values, jsonText(t, value))
		mu.Unlock()
	})
	if err := readyErr(t, chatRef); err != nil {
		t.Fatal(err)
	}
	mustCall(t, requestsGet(t, server, conv, requestsEmpty(), nil), "set", "hi", nil)
	waitFor(t, "the view to follow", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(values) > 0 && values[len(values)-1] == `{"text":"hi"}`
	})
	if got := jsonText(t, view.Value()); got != `{"text":"hi"}` {
		t.Fatalf("chat.view.value = %s", got)
	}

	// Across a link: the snapshot and its ops reach a browser.
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	remoteChat := requestsGet(t, browser, chat, requestsEmpty(), nil)
	if err := readyErr(t, remoteChat); err != nil {
		t.Fatal(err)
	}
	remoteView := requestsReadable(t, remoteChat, "view")
	mustCall(t, requestsGet(t, server, conv, requestsEmpty(), nil), "set", "there", nil)
	waitFor(t, "the remote ops", func() bool { return jsonText(t, remoteView.Value()) == `{"text":"there"}` })
}

// requests.test.ts:93-133 "#2 facets reach their own host through FacetApi.root and FacetApi.registry"
func TestRequests2FacetsReachTheirOwnHostThroughFacetApiRootAndRegistry(t *testing.T) {
	config := Registry[any](DefineRegistry[string]("t.config"))
	settings := Local("t.settings", LocalDef{Version: 1, Input: tObject()})
	reader := Local("t.reader", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"read", Method(nil, tString())}}})
	var mu sync.Mutex
	var captured []FacetApi
	facet := func(value string) *Facet {
		return &Facet{Name: "config", FacetDefinition: DefineFacet(func(api FacetApi) error {
			mu.Lock()
			captured = append(captured, api)
			mu.Unlock()
			if err := api.Serve(settings, func(_ any, _ Principal, self Self) (Implementation, error) {
				if err := self.Register(config, []Entry{{Name: "greeting", Value: value}}); err != nil {
					return nil, err
				}
				return chordjson.NewObject(0), nil
			}, ServeOptions{Lifetime: LifetimeHost}); err != nil {
				return err
			}
			return api.Serve(reader, func(any, Principal, Self) (Implementation, error) {
				return chordjson.ObjectOf("read", methodFunc(func(_ any, ctx Ctx) (any, error) {
					if greeting, ok := api.Registry(config).Get("greeting", ctx); ok {
						return greeting, nil
					}
					return "", nil
				})), nil
			}, ServeOptions{})
		}, DefineFacetOptions{})}
	}
	host := requestsHost(t, HostOptions{Name: "worker"})
	if err := host.Install(*facet("v1"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	api := captured[0]
	mu.Unlock()
	if api.Name != "worker" {
		t.Fatalf("api.Name = %q", api.Name)
	}
	read := func() any {
		// A callback outside Chord (a Durable tool, say) starts its own root.
		value, err := api.Root(func(ctx Ctx) (any, error) {
			ref, err := ctx.Get(reader, requestsEmpty(), nil)
			if err != nil {
				return nil, err
			}
			return callMember(ref, "read", nil, ctx)
		}, RootOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	if got := read(); got != "v1" {
		t.Fatalf("read = %v", got)
	}
	if _, err := host.Reload([]PluginRef{facet("v2")}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	next := captured[1]
	mu.Unlock()
	if got, _ := next.Registry(config).Get("greeting", nil); got != "v2" {
		t.Fatalf("registry after reload = %v", got)
	}
	if got := read(); got != "v2" {
		t.Fatalf("read after reload = %v", got)
	}
}

// singleWatch is a source that accepts one subscriber at a time (like a Durable watch).
type singleWatch struct {
	mu          sync.Mutex
	subscribers int
	emit        func(event any)
}

func (watch *singleWatch) start(fn func(event any)) (func(), error) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.subscribers > 0 {
		return nil, errors.New("Watch is already started")
	}
	watch.subscribers++
	watch.emit = fn
	return func() {
		watch.mu.Lock()
		watch.subscribers--
		watch.mu.Unlock()
	}, nil
}

func (watch *singleWatch) send(event any) {
	watch.mu.Lock()
	emit := watch.emit
	watch.mu.Unlock()
	emit(event)
}

// subscribeFunc adapts a subscribe function to Subscribable.
type subscribeFunc func(fn func(event any)) func()

func (subscribe subscribeFunc) Subscribe(fn func(event any)) func() { return subscribe(fn) }

// requests.test.ts:135-182 "#3 a listener keeps its subscription across reloads when resource and select are
// unchanged"
func TestRequests3AListenerKeepsItsSubscriptionAcrossReloadsWhenResourceAndSelectAreUnchanged(t *testing.T) {
	seenSchema := tArray(tString())
	watcher := Local("t.watcher", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"seen", seenSchema}}})
	single := &singleWatch{}
	facet := func(tag string) *Facet {
		return &Facet{Name: "watcher", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(watcher, func(_ any, _ Principal, self Self) (Implementation, error) {
				seen, err := self.State("seen", seenSchema, []any{}, nil)
				if err != nil {
					return nil, err
				}
				watchResource, err := self.Resource("watch", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { return single, nil },
				})
				if err != nil {
					return nil, err
				}
				// A new adapter object per call, and a new arrow per factory run.
				err = self.OnSelect("events", watchResource,
					func(watch any) Subscribable[any] {
						return subscribeFunc(func(fn func(event any)) func() {
							stop, err := watch.(*singleWatch).start(fn)
							if err != nil {
								panic(err)
							}
							return stop
						})
					},
					func(event any, ctx Ctx) error {
						return seen.Change(ctx, func(draft any) {
							_, _ = draft.(*delta.Array).Push(tag + ":" + event.(string))
						})
					})
				if err != nil {
					return nil, err
				}
				return chordjson.ObjectOf("seen", seen), nil
			}, ServeOptions{})
		}}}
	}
	host := requestsHost(t, HostOptions{})
	if err := host.Install(*facet("v1"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := requestsGet(t, host, watcher, requestsEmpty(), nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	single.send("a")
	for _, tag := range []string{"v2", "v3"} {
		if _, err := host.Reload([]PluginRef{facet(tag)}, ReloadOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	single.send("b")
	seen := requestsReadable(t, ref, "seen")
	waitFor(t, "two events", func() bool { return jsonText(t, seen.Value()) == `["v1:a","v3:b"]` })
	single.mu.Lock()
	defer single.mu.Unlock()
	if single.subscribers != 1 {
		t.Fatalf("subscribers = %d", single.subscribers)
	}
}

type countingSource struct {
	mu         sync.Mutex
	subscribes int
}

func (source *countingSource) Subscribe(func(event any)) func() {
	source.mu.Lock()
	source.subscribes++
	source.mu.Unlock()
	return func() {}
}

func (source *countingSource) count() int {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.subscribes
}

// requests.test.ts:184-208 "#3 a changed select resubscribes"
func TestRequests3AChangedSelectResubscribes(t *testing.T) {
	watcher := Local("t.watcher2", LocalDef{Version: 1, Input: tObject()})
	source := &countingSource{}
	facet := func(selectText bool) *Facet {
		return &Facet{Name: "watcher2", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(watcher, func(_ any, _ Principal, self Self) (Implementation, error) {
				sResource, err := self.Resource("s", ResourceDeclaration[any]{
					Version: 1,
					Open:    func(ResourceOpenOptions) (any, error) { return source, nil },
				})
				if err != nil {
					return nil, err
				}
				selector := func(value any) Subscribable[any] { return value.(Subscribable[any]) }
				if !selectText {
					selector = func(s any) Subscribable[any] { return s.(*countingSource) }
				}
				err = self.OnSelect("s", sResource, selector, func(any, Ctx) error { return nil })
				return chordjson.NewObject(0), err
			}, ServeOptions{})
		}}}
	}
	host := requestsHost(t, HostOptions{})
	if err := host.Install(*facet(true), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, requestsGet(t, host, watcher, requestsEmpty(), nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Reload([]PluginRef{facet(true)}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := source.count(); got != 1 {
		t.Fatalf("subscribes after an unchanged reload = %d", got)
	}
	if _, err := host.Reload([]PluginRef{facet(false)}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := source.count(); got != 2 {
		t.Fatalf("subscribes after a changed select = %d", got)
	}
}

// requests.test.ts:210-254 "#4 replace diffs a new revision that shares structure; unrelated values replace"
func TestRequests4ReplaceDiffsANewRevisionThatSharesStructureUnrelatedValuesReplace(t *testing.T) {
	mirror := Local("t.mirror", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{
		{"doc", tUnknown()},
		{"set", Method(tUnknown(), nil)},
	}})
	docSchema := tObject("entries", tArray(tString()), "status", tString())
	var optionsMu sync.Mutex
	var lastOptions CellReplaceOptions
	host := requestsHost(t, HostOptions{})
	requestsServe(t, host.Serve, mirror, func(_ any, _ Principal, self Self) (Implementation, error) {
		doc, err := self.State("doc", docSchema, delta.JsonObjectOf("entries", []any{"a", "b"}, "status", "idle"), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf(
			"doc", doc,
			"set", methodFunc(func(value any, ctx Ctx) (any, error) {
				optionsMu.Lock()
				options := lastOptions
				optionsMu.Unlock()
				return nil, doc.Replace(ctx, value, options)
			}),
		), nil
	}, ServeOptions{})
	ref := requestsGet(t, host, mirror, requestsEmpty(), nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	doc := requestsReadable(t, ref, "doc")
	var mu sync.Mutex
	var events []StateEvent[any]
	var replica any
	doc.Events(func(event StateEvent[any]) {
		mu.Lock()
		defer mu.Unlock()
		if event.Type == StateEventOps {
			next, err := delta.ApplyImmutable(replica, event.Ops)
			if err != nil {
				t.Error(err)
			}
			replica = next
		} else {
			replica = event.Value
		}
		events = append(events, event)
	})
	lastOps := func() []delta.Op {
		mu.Lock()
		defer mu.Unlock()
		if len(events) == 0 || events[len(events)-1].Type != StateEventOps {
			return nil
		}
		return events[len(events)-1].Ops
	}
	firstVerb := func() string {
		if ops := lastOps(); len(ops) > 0 {
			return delta.Verb(ops[0])
		}
		return ""
	}
	set := func(value any) {
		t.Helper()
		mustCall(t, ref, "set", value, nil)
	}
	current := doc.Value().(*chordjson.Object)
	// A new revision reusing entries: a diff.
	set(delta.JsonObjectOf("entries", current.Value("entries"), "status", "running"))
	if got := jsonText(t, lastOps()); got != `[["s",["status"],"running"]]` {
		t.Fatalf("shared-structure ops = %s", got)
	}
	// Unrelated containers: one replacement op.
	set(delta.JsonObjectOf("entries", []any{"a", "b", "c"}, "status", "running"))
	if got := firstVerb(); got != "r" {
		t.Fatalf("unrelated value verb = %q", got)
	}
	// Forced either way.
	yes, no := true, false
	optionsMu.Lock()
	lastOptions = CellReplaceOptions{Diff: &yes}
	optionsMu.Unlock()
	set(delta.JsonObjectOf("entries", []any{"a", "b", "c", "d"}, "status", "running"))
	if got := firstVerb(); got == "r" {
		t.Fatal("forced diff produced a replacement op")
	}
	optionsMu.Lock()
	lastOptions = CellReplaceOptions{Diff: &no}
	optionsMu.Unlock()
	shared := doc.Value().(*chordjson.Object)
	set(delta.JsonObjectOf("entries", shared.Value("entries"), "status", "done"))
	if got := firstVerb(); got != "r" {
		t.Fatalf("forced replace verb = %q", got)
	}
	waitFor(t, "the replica", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return jsonText(t, replica) == jsonText(t, doc.Value())
	})
}

// requests.test.ts:256-293 "#5 roots of a child that connected behind the server are admitted in the server's code version"
func TestRequests5RootsOfAChildThatConnectedBehindTheServerAreAdmittedInTheServersCodeVersion(t *testing.T) {
	echo := Remote("t.echo", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"codeVersion", Method(nil, tNumber())}},
	}}}})
	server := requestsHost(t, HostOptions{Name: "server"})
	echoFacet := &Facet{Name: "echo", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(echo, func(any, Principal, Self) (Implementation, error) {
			return chordjson.ObjectOf("variant", "main", "codeVersion", methodFunc(func(_ any, ctx Ctx) (any, error) {
				return float64(ctx.CodeVersion()), nil
			})), nil
		}, ServeOptions{})
	}}}
	if err := server.Install(*echoFacet, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Reload([]PluginRef{echoFacet}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if server.CodeVersion() != 2 {
		t.Fatalf("server code version = %d", server.CodeVersion())
	}
	if previous, ok := server.PreviousCodeVersion(); ok {
		t.Fatalf("previous code version = %d", previous)
	}
	worker := requestsHost(t, HostOptions{Name: "session:S1", Loader: func([]string) ([]Facet, error) { return nil, nil }})
	gate := newRequestsLatch()
	type outcome struct {
		value any
		err   error
	}
	result := make(chan outcome, 1)
	// A worker root of code version 1 (an eager object's listener, say) calls the server after connecting. In Pi the root
	// has started when worker.root returns; here it has once fn runs, so the worker connects only after that.
	entered := make(chan struct{})
	go func() {
		value, err := worker.Root(func(ctx Ctx) (any, error) {
			close(entered)
			gate.wait()
			ref, err := ctx.Get(echo, requestsEmpty(), nil)
			if err != nil {
				return nil, err
			}
			return callMember(ref, "codeVersion", nil, ctx)
		}, RootOptions{})
		result <- outcome{value, err}
	}()
	select {
	case <-entered:
	case got := <-result:
		t.Fatalf("the worker's root ended before it started: %v, %v", got.value, got.err)
	}
	down, up := MemoryLinkPair()
	name := "session:S1"
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"worker": "S1"}, Kind: KindHost, Name: &name}); err != nil {
		t.Fatal(err)
	}
	worker.Connect(up, ConnectOptions{Principal: kindServer()})
	gate.open()
	got := <-result
	if got.err != nil || got.value != float64(2) {
		t.Fatalf("call = %v, %v", got.value, got.err)
	}
	waitFor(t, "the worker to reach code version 2", func() bool { return worker.CodeVersion() == 2 })
	// Unknown code versions from in-step peers stay superseded.
	one := 1
	if _, err := server.Root(func(Ctx) (any, error) { return float64(0), nil }, RootOptions{CodeVersion: &one}); requestsChordCode(err) != CodeSuperseded {
		t.Fatalf("root of code version 1 = %v", err)
	}
}

type queryLog struct{ queries []string }

// foreignResource is a Resource that no object declared.
type foreignResource struct{}

func (foreignResource) Key() string         { return "x" }
func (foreignResource) Value() (any, error) { return float64(1), nil }

// requests.test.ts:295-323 "#6 self.expose: an opaque member backed by a resource, readable once live"
func TestRequests6SelfExposeAnOpaqueMemberBackedByAResourceReadableOnceLive(t *testing.T) {
	store := Local("t.store", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"db", Opaque("t.db@1")}}})
	gate := newRequestsLatch()
	native := &queryLog{}
	host := requestsHost(t, HostOptions{})
	requestsServe(t, host.Serve, store, func(_ any, _ Principal, self Self) (Implementation, error) {
		db, err := self.Resource("db", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				gate.wait()
				return native, nil
			},
		})
		if err != nil {
			return nil, err
		}
		exposed, err := self.Expose(db)
		return chordjson.ObjectOf("db", exposed), err
	}, ServeOptions{})
	ref := requestsGet(t, host, store, requestsEmpty(), nil)
	if got := ref.Member("db"); got != nil {
		t.Fatalf("db before live = %v", got)
	}
	gate.open()
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if got := ref.Member("db"); got != any(native) {
		t.Fatalf("db when live = %v", got)
	}

	other := Local("t.other", LocalDef{Version: 1, Input: tObject()})
	requestsServe(t, host.Serve, other, func(_ any, _ Principal, self Self) (Implementation, error) {
		if _, err := self.Expose(foreignResource{}); err != nil {
			return nil, err
		}
		return chordjson.NewObject(0), nil
	}, ServeOptions{})
	err := readyErr(t, requestsGet(t, host, other, requestsEmpty(), nil))
	if err == nil || !strings.Contains(err.Error(), "expose takes a resource declared by this object") {
		t.Fatalf("expose of a foreign resource = %v", err)
	}
}

// requests.test.ts:325-353 "#7 a cluster reload reports what each child prepared"
func TestRequests7AClusterReloadReportsWhatEachChildPrepared(t *testing.T) {
	greeterType := Remote("t.greeter", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("session:S1"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"greet", Method(tString(), tString())}},
	}}}})
	greeter := func(text string) Facet {
		return Facet{Name: "greeter", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
			return api.Serve(greeterType, func(any, Principal, Self) (Implementation, error) {
				return chordjson.ObjectOf("variant", "main", "greet", methodFunc(func(name any, _ Ctx) (any, error) {
					return text + " " + name.(string), nil
				})), nil
			}, ServeOptions{})
		}}}
	}
	var modulesMu sync.Mutex
	modules := map[string]Facet{"greeter": greeter("v1")}
	loader := func(names []string) ([]Facet, error) {
		modulesMu.Lock()
		defer modulesMu.Unlock()
		facets := make([]Facet, len(names))
		for index, name := range names {
			facets[index] = modules[name]
		}
		return facets, nil
	}
	server := requestsHost(t, HostOptions{Name: "server"})
	down, up := MemoryLinkPair()
	name := "session:S1"
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"worker": "S1"}, Kind: KindHost, Name: &name}); err != nil {
		t.Fatal(err)
	}
	worker := requestsHost(t, HostOptions{Name: name, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, Loader: loader})
	if err := worker.Install(greeter("v1"), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := requestsGet(t, server, greeterType, requestsEmpty(), nil)
	if got := mustCall(t, ref, "greet", "a", nil); got != "v1 a" {
		t.Fatalf("greet = %v", got)
	}
	modulesMu.Lock()
	modules["greeter"] = greeter("v2")
	modulesMu.Unlock()
	report, err := server.Reload(nil, ReloadOptions{Remote: map[string][]string{"session:": {"greeter"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changed) != 0 {
		t.Fatalf("server changed = %v", report.Changed)
	}
	child, ok := report.Children[name]
	if !ok || !slices.Equal(child.Changed, []string{"t.greeter"}) || len(child.Prepared) != 1 || child.CodeVersion != report.CodeVersion {
		t.Fatalf("child report = %+v (present %v), server code version %d", child, ok, report.CodeVersion)
	}
}

// requests.test.ts:362-396 "#8 a held remote reference keeps its state without listeners; a later subscriber sees the
// current value"
func TestRequests8AHeldRemoteReferenceKeepsItsStateWithoutListeners(t *testing.T) {
	counter := Remote("t.count8", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		State:   Fields[Schema]{{"n", tNumber()}},
		Methods: Fields[MethodSpec]{{"set", Method(tNumber(), nil)}},
	}}}})
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, counter, func(_ any, _ Principal, self Self) (Implementation, error) {
		n, err := self.State("n", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("variant", "main", "n", n, "set", methodFunc(func(value any, ctx Ctx) (any, error) {
			return nil, n.Replace(ctx, value, CellReplaceOptions{})
		})), nil
	}, ServeOptions{})
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	ref := requestsGet(t, browser, counter, requestsEmpty(), nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	n := requestsReadable(t, ref, "n")
	if got := n.Value(); got != float64(0) {
		t.Fatalf("hydrated value = %v, want 0", got)
	}
	stop := n.Subscribe(func(any) {})
	mustCall(t, ref, "set", float64(1), nil)
	waitFor(t, "n = 1", func() bool { return n.Value() == float64(1) })
	stop()
	mustCall(t, ref, "set", float64(2), nil)
	waitFor(t, "n = 2", func() bool { return n.Value() == float64(2) })
	var mu sync.Mutex
	var seen []any
	again := n.Subscribe(func(value any) {
		mu.Lock()
		seen = append(seen, value)
		mu.Unlock()
	})
	mu.Lock()
	if !slices.Equal(seen, []any{float64(2)}) {
		t.Errorf("seen = %v", seen)
	}
	mu.Unlock()
	again()
}

type statusLog struct {
	mu      sync.Mutex
	entries []string
}

func (log *statusLog) add(entry string) {
	log.mu.Lock()
	log.entries = append(log.entries, entry)
	log.mu.Unlock()
}

func (log *statusLog) list() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return slices.Clone(log.entries)
}

// requests.test.ts:389-422 "#9 references report status changes: live, loading while a link reconnects, gone, released"
func TestRequests9RefsReportStatusChanges(t *testing.T) {
	thing := Remote("t.thing9", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{}}}})
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, thing, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main"), nil
	}, ServeOptions{})
	// Pi's get and onStatus run in one synchronous section: the activation, a later microtask, cannot report live
	// before the listener is registered. The Go test runs them as one section on the server's loop.
	var localThing Ref
	var local statusLog
	server.run(func() {
		localThing = requestsGet(t, server, thing, requestsEmpty(), &GetOptions{Principal: chordjson.ObjectOf("user", "u"), HasPrincipal: true})
		localThing.OnStatus(func(status RefStatus, err error) {
			if status == RefGone {
				local.add(string(status) + ":" + string(requestsChordCode(err)))
				return
			}
			local.add(string(status))
		})
	})
	if err := readyErr(t, localThing); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "live", func() bool { return len(local.list()) == 1 })
	if err := server.InvalidatePrincipal(PrincipalMatch{Pattern: chordjson.ObjectOf("user", "u")}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "gone", func() bool { return len(local.list()) == 2 })
	if got := local.list(); !slices.Equal(got, []string{"live", "gone:gone"}) {
		t.Fatalf("local statuses = %v", got)
	}

	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser := requestsHost(t, HostOptions{Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}})
	var remoteThing Ref
	var remote statusLog
	browser.run(func() {
		remoteThing = requestsGet(t, browser, thing, requestsEmpty(), nil)
		remoteThing.OnStatus(func(status RefStatus, _ error) { remote.add(string(status)) })
	})
	if err := readyErr(t, remoteThing); err != nil {
		t.Fatal(err)
	}
	up.Close()
	waitFor(t, "loading", func() bool { return len(remote.list()) == 2 })
	down2, up2 := MemoryLinkPair()
	if err := server.Accept(down2, AcceptOptions{Principal: map[string]any{"guest": "g"}}); err != nil {
		t.Fatal(err)
	}
	browser.Connect(up2, ConnectOptions{Principal: kindServer()})
	waitFor(t, "live again", func() bool { return len(remote.list()) == 3 })
	remoteThing.Release()
	waitFor(t, "released", func() bool { return len(remote.list()) == 4 })
	if got := remote.list(); !slices.Equal(got, []string{"live", "loading", "live", "released"}) {
		t.Fatalf("remote statuses = %v", got)
	}
}

type tracker struct {
	mu      sync.Mutex
	entries map[string]bool
}

// requests.test.ts:424-477 "#10 host close disposes holders before what they hold, and before closing links"
func TestRequests10HostCloseDisposesHoldersBeforeWhatTheyRefAndBeforeClosingLinks(t *testing.T) {
	var logMu sync.Mutex
	var log []string
	push := func(entry string) {
		logMu.Lock()
		log = append(log, entry)
		logMu.Unlock()
	}
	remoteType := Remote("t.remote10", RemoteDef{Version: 1, Input: tObject(), Host: NewHostNamed("server"), Variants: Fields[VariantDef]{{"main", VariantDef{
		Methods: Fields[MethodSpec]{{"note", Method(tString(), nil)}},
	}}}})
	server := requestsHost(t, HostOptions{Name: "server"})
	requestsServe(t, server.Serve, remoteType, func(any, Principal, Self) (Implementation, error) {
		return chordjson.ObjectOf("variant", "main", "note", methodFunc(func(text any, _ Ctx) (any, error) {
			push("server: " + text.(string))
			return nil, nil
		})), nil
	}, ServeOptions{})
	proc := Local("t.proc", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"tracker", Opaque("t.tracker@1")}}})
	chat := Local("t.chat10", LocalDef{Version: 1, Input: tObject()})
	down, up := MemoryLinkPair()
	name := "w"
	if err := server.Accept(down, AcceptOptions{Principal: map[string]any{"worker": "w"}, Kind: KindHost, Name: &name}); err != nil {
		t.Fatal(err)
	}
	worker := CreateHost(HostOptions{Self: kindHost(), Name: "w", Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: kindServer()}}, OnError: func(err error) { push("error: " + err.Error()) }})
	// Host lifetime: activated (and registered with the host) before the object that holds it.
	requestsServe(t, worker.Serve, proc, func(_ any, _ Principal, self Self) (Implementation, error) {
		trackerResource, err := self.Resource("tracker", ResourceDeclaration[any]{
			Version: 1,
			Open:    func(ResourceOpenOptions) (any, error) { return &tracker{entries: map[string]bool{}}, nil },
			Close:   func(any) error { push("proc closed"); return nil },
		})
		if err != nil {
			return nil, err
		}
		exposed, err := self.Expose(trackerResource)
		return chordjson.ObjectOf("tracker", exposed), err
	}, ServeOptions{Lifetime: LifetimeHost})
	requestsServe(t, worker.Serve, chat, func(_ any, _ Principal, self Self) (Implementation, error) {
		procRef, err := self.Get(proc, requestsEmpty(), nil)
		if err != nil {
			return nil, err
		}
		upstream, err := self.Get(remoteType, requestsEmpty(), nil)
		if err != nil {
			return nil, err
		}
		trackerOf := func() *tracker {
			value, _ := procRef.Member("tracker").(*tracker)
			return value
		}
		_, err = self.Resource("session", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				if current := trackerOf(); current != nil {
					current.mu.Lock()
					current.entries["chat"] = true
					current.mu.Unlock()
				}
				return nil, nil
			},
			Close: func(any) error {
				current := trackerOf()
				state := "gone"
				if current != nil {
					current.mu.Lock()
					delete(current.entries, "chat")
					current.mu.Unlock()
					state = "ok"
				}
				push("chat closed, proc " + string(procRef.Status()) + ", tracker " + state)
				_, err := callMember(upstream, "note", "bye", nil)
				return err
			},
		})
		return chordjson.NewObject(0), err
	}, ServeOptions{})
	if err := readyErr(t, requestsGet(t, worker, chat, requestsEmpty(), nil)); err != nil {
		t.Fatal(err)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	logMu.Lock()
	defer logMu.Unlock()
	if want := []string{"chat closed, proc live, tracker ok", "server: bye", "proc closed"}; !slices.Equal(log, want) {
		t.Fatalf("log = %q, want %q", log, want)
	}
}
