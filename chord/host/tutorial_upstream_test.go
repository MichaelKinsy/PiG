package host

// Ports packages/chord/test/tutorial.test.ts, the runnable examples of Pi's docs/tutorial.md. Pi's checkExamples
// (tutorial.test.ts:44-52) compares every TypeScript example printed in docs/tutorial.md with this file's TypeScript
// source; a Go port has no TypeScript source to compare, so that check is TestTutorialExamplesMatchTheirRunnableSource
// (skipped) and every case below runs its example's code only.

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// field reads a member of a decoded JSON object (input, args).
func field(value any, name string) any {
	switch value := value.(type) {
	case *chordjson.Object:
		return value.Value(name)
	case map[string]any:
		return value[name]
	}
	return nil
}

// tutorial.test.ts:55-60 TodoLabel.
var tutorialTodoLabel = Local("tutorial.todos", LocalDef{
	Version: 1,
	Input:   tObject("list", tString()),
	Members: Fields[any]{{"label", Method(nil, tString())}},
})

// tutorial.test.ts:63-77 Todo, Rows, TodoStore.
var (
	tutorialTodo      = tObject("text", tString(), "done", tBoolean())
	tutorialRows      = tArray(tutorialTodo)
	tutorialTodoStore = Local("tutorial.todos", LocalDef{
		Version: 1,
		Input:   tObject("list", tString()),
		Members: Fields[any]{
			{"label", Method(nil, tString())},
			{"rows", tutorialRows},
			{"openCount", tNumber()},
			{"add", Method(tObject("text", tString()), nil)},
			{"finish", Method(tObject("index", Schema{"type": "integer"}), nil)},
			{"reset", Method(tutorialRows, nil)},
		},
	})
)

// todoStore is tutorial.test.ts todoStore: the store's factory; options versions its rows (section 9).
func todoStore(options *StateOptions) Factory {
	return func(input any, _ Principal, self Self) (Implementation, error) {
		return todoStoreImplementation(options, input, self)
	}
}

// todoFactory is tutorial.test.ts todoFactory, todoStore().
var todoFactory = todoStore(nil)

func todoStoreImplementation(options *StateOptions, input any, self Self) (Implementation, error) {
	list := field(input, "list").(string)
	rows, err := self.State("rows", tutorialRows, []any{}, options)
	if err != nil {
		return nil, err
	}
	openCount, err := self.Derive(Sources{rows}, func(values ...any) any {
		open := 0
		for _, todo := range values[0].([]any) {
			if field(todo, "done") != true {
				open++
			}
		}
		return float64(open)
	})
	if err != nil {
		return nil, err
	}
	return chordjson.ObjectOf(
		"label", methodFunc(func(any, Ctx) (any, error) { return "Todos for " + list, nil }),
		"rows", rows,
		"openCount", openCount,
		"add", methodFunc(func(args any, ctx Ctx) (any, error) {
			text := field(args, "text")
			return nil, rows.Change(ctx, func(draft any) {
				if _, err := draft.(*delta.Array).Push(delta.JsonObjectOf("text", text, "done", false)); err != nil {
					panic(err)
				}
			})
		}),
		"finish", methodFunc(func(args any, ctx Ctx) (any, error) {
			index := int(field(args, "index").(float64))
			return nil, rows.Change(ctx, func(draft any) {
				todos := draft.(*delta.Array)
				if index < 0 || index >= todos.Len() {
					panic(Invalid("args", "No such todo", nil))
				}
				if err := todos.Object(index).Set("done", true); err != nil {
					panic(err)
				}
			})
		}),
		"reset", methodFunc(func(args any, ctx Ctx) (any, error) {
			return nil, rows.Replace(ctx, args, CellReplaceOptions{Diff: new(true)})
		}),
	), nil
}

// tutorial.test.ts:112-141 Todos and serveViews.
var tutorialTodos = Remote("tutorial.todo-view", RemoteDef{
	Version: 1,
	Input:   tObject("list", tString()),
	Host:    NewHostNamed("server"),
	Variants: Fields[VariantDef]{
		{"viewer", VariantDef{State: Fields[Schema]{{"rows", tutorialRows}}}},
		{"editor", VariantDef{
			State:   Fields[Schema]{{"rows", tutorialRows}},
			Methods: Fields[MethodSpec]{{"add", Method(tObject("text", tString()), nil)}},
		}},
	},
})

func serveViews(t *testing.T, serve Serve) {
	t.Helper()
	err := serve(tutorialTodos, func(input any, principal Principal, self Self) (Implementation, error) {
		list := field(input, "list")
		if list != "family" {
			return nil, Forbidden("This list is not shared")
		}
		store, err := self.Get(tutorialTodoStore, chordjson.ObjectOf("list", list), nil)
		if err != nil {
			return nil, err
		}
		if principal == "guest" {
			return chordjson.ObjectOf("variant", "viewer", "rows", store.Member("rows")), nil
		}
		return chordjson.ObjectOf(
			"variant", "editor",
			"rows", store.Member("rows"),
			"add", methodFunc(func(args any, ctx Ctx) (any, error) {
				text, _ := field(args, "text").(string)
				if len(strings.TrimSpace(text)) == 0 {
					return nil, Invalid("args", "A todo needs text", nil)
				}
				return callMember(store, "add", chordjson.ObjectOf("text", text), ctx)
			}),
		), nil
	}, ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// mustGetRef is `await host.get(...)` (tutorial.test.ts, b45e0ebc): the reference once it is ready.
func mustGetRef(t *testing.T, host *HostImpl, typ *Contract, input any, options *GetOptions) Ref {
	t.Helper()
	ref, err := host.Get(typ, input, options)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func readable(t *testing.T, ref Ref, name string) Readable[any] {
	t.Helper()
	member, ok := ref.Member(name).(Readable[any])
	if !ok {
		t.Fatalf("%s is not state", name)
	}
	return member
}

func family() *chordjson.Object { return chordjson.ObjectOf("list", "family") }

func text(value string) *chordjson.Object { return chordjson.ObjectOf("text", value) }

func principalOption(principal Principal) *GetOptions {
	return &GetOptions{Principal: principal, HasPrincipal: true}
}

func closeHost(t *testing.T, host *HostImpl) {
	t.Helper()
	t.Cleanup(func() { _ = host.Close() })
}

// signal is Pi's Promise.withResolvers<void>() as a one-shot channel.
type signal struct {
	once sync.Once
	ch   chan struct{}
}

func newSignal() *signal   { return &signal{ch: make(chan struct{})} }
func (s *signal) resolve() { s.once.Do(func() { close(s.ch) }) }
func (s *signal) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out (Pi's per-test timeout is 5000 ms)")
	}
}

// tutorial.test.ts:44-52 checkExamples, called first by every case.
func TestTutorialExamplesMatchTheirRunnableSource(t *testing.T) {
	t.Skip("Pi tutorial.test.ts:44: checkExamples compares docs/tutorial.md's TypeScript examples with the TypeScript " +
		"source of tutorial.test.ts; the Go port has no TypeScript source to compare")
}

// tutorial.test.ts:143-150 "01: objects live inside hosts".
func TestTutorial01ObjectsLiveInsideHosts(t *testing.T) {
	host := CreateHost(HostOptions{Self: "server", Name: "todo-server"})
	if host.Name() != "todo-server" {
		t.Fatalf("name = %q", host.Name())
	}
	if err := host.Close(); err != nil {
		t.Fatal(err)
	}
}

// tutorial.test.ts:152-164 "02: first contract, factory, reference and root".
func TestTutorial02FirstContractFactoryRefAndRoot(t *testing.T) {
	host := CreateHost(HostOptions{Self: "server"})
	closeHost(t, host)
	if err := host.Serve(tutorialTodoLabel, func(input any, _ Principal, _ Self) (Implementation, error) {
		list := field(input, "list").(string)
		return chordjson.ObjectOf("label", methodFunc(func(any, Ctx) (any, error) { return "Todos for " + list, nil })), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := host.Root(func(ctx Ctx) (any, error) {
		todos, err := ctx.Get(tutorialTodoLabel, family(), nil)
		if err != nil {
			return nil, err
		}
		return callMember(todos, "label", nil, ctx)
	}, RootOptions{})
	if err != nil || result != "Todos for family" {
		t.Fatalf("result = %v, %v", result, err)
	}
}

// tutorial.test.ts:166-186 "03: cells publish values and derivations".
func TestTutorial03CellsPublishValuesAndDerivations(t *testing.T) {
	host := CreateHost(HostOptions{Self: "server", Dev: true})
	closeHost(t, host)
	if err := host.Serve(tutorialTodoStore, todoFactory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	todos := mustGetRef(t, host, tutorialTodoStore, family(), nil)
	if err := readyErr(t, todos); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var counts []any
	stop := readable(t, todos, "openCount").Subscribe(func(value any) {
		mu.Lock()
		counts = append(counts, value)
		mu.Unlock()
	})
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		if _, err := callMember(todos, "add", text("Buy milk"), ctx); err != nil {
			return nil, err
		}
		if _, err := callMember(todos, "finish", chordjson.ObjectOf("index", float64(0)), ctx); err != nil {
			return nil, err
		}
		return callMember(todos, "reset", []any{}, ctx)
	}, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if got := jsonText(t, counts); got != "[0,1,0]" {
		t.Fatalf("counts = %s", got)
	}
	mu.Unlock()
	if got := jsonText(t, readable(t, todos, "rows").Value()); got != "[]" {
		t.Fatalf("rows = %s", got)
	}
	stop()
	todos.Release()
}

type tutorialDatabase struct {
	mu    sync.Mutex
	saved []string
}

// tutorial.test.ts:188-252 "04: holds, asynchronous resources and release".
func TestTutorial04RefsAsynchronousResourcesAndRelease(t *testing.T) {
	database := Local("tutorial.database", LocalDef{
		Version: 1,
		Input:   tObject(),
		Members: Fields[any]{{"db", Opaque("tutorial.database@1")}},
	})
	savedTodos := Local("tutorial.saved-todos", LocalDef{
		Version: 1,
		Input:   tObject("list", tString()),
		Members: Fields[any]{{"add", Method(tObject("text", tString()), nil)}},
	})
	host := CreateHost(HostOptions{Self: "server"})
	closeHost(t, host)
	var opens int
	var opensMu sync.Mutex
	closed := newSignal()
	if err := host.Serve(database, func(_ any, _ Principal, self Self) (Implementation, error) {
		db, err := self.Resource("db", ResourceDeclaration[any]{
			Version: 1,
			Open: func(ResourceOpenOptions) (any, error) {
				opensMu.Lock()
				opens++
				opensMu.Unlock()
				return &tutorialDatabase{saved: []string{}}, nil
			},
			Close: func(any) error {
				closed.resolve()
				return nil
			},
		})
		if err != nil {
			return nil, err
		}
		exposed, err := self.Expose(db)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("db", exposed), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := host.Serve(tutorialTodoStore, todoFactory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := host.Serve(savedTodos, func(input any, _ Principal, self Self) (Implementation, error) {
		db, err := self.Get(database, chordjson.ObjectOf(), nil)
		if err != nil {
			return nil, err
		}
		todos, err := self.Get(tutorialTodoStore, chordjson.ObjectOf("list", field(input, "list")), nil)
		if err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("add", methodFunc(func(args any, ctx Ctx) (any, error) {
			value := db.Member("db").(*tutorialDatabase)
			value.mu.Lock()
			value.saved = append(value.saved, field(args, "text").(string))
			value.mu.Unlock()
			if _, err := callMember(todos, "add", text(field(args, "text").(string)), ctx); err != nil {
				return nil, err
			}
			return nil, nil
		})), nil
	}, ServeOptions{Linger: 0}); err != nil {
		t.Fatal(err)
	}
	saved := mustGetRef(t, host, savedTodos, family(), nil)
	db := mustGetRef(t, host, database, chordjson.ObjectOf(), nil)
	if err := readyErr(t, saved); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Root(func(ctx Ctx) (any, error) { return callMember(saved, "add", text("Buy milk"), ctx) }, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	value, ok := db.Member("db").(*tutorialDatabase)
	if !ok {
		t.Fatalf("db = %#v", db.Member("db"))
	}
	value.mu.Lock()
	if !slices.Equal(value.saved, []string{"Buy milk"}) {
		t.Fatalf("saved = %v", value.saved)
	}
	value.mu.Unlock()
	opensMu.Lock()
	if opens != 1 {
		t.Fatalf("opens = %d", opens)
	}
	opensMu.Unlock()
	var statusMu sync.Mutex
	var statuses []RefStatus
	saved.OnStatus(func(status RefStatus, _ error) {
		statusMu.Lock()
		statuses = append(statuses, status)
		statusMu.Unlock()
	})
	db.Release()
	saved.Release()
	closed.wait(t)
	if saved.Status() != RefReleased {
		t.Fatalf("status = %s", saved.Status())
	}
	statusMu.Lock()
	defer statusMu.Unlock()
	if !slices.Contains(statuses, RefReleased) {
		t.Fatalf("statuses = %v", statuses)
	}
}

// principalString is tutorial.test.ts's String(principal).
func principalString(principal Principal) string {
	if text, ok := principal.(string); ok {
		return text
	}
	return "[object Object]"
}

// tutorial.test.ts:254-271 "05: principals select views of one shared store".
func TestTutorial05PrincipalsSelectViewsOfOneSharedStore(t *testing.T) {
	host := CreateHost(HostOptions{Name: "server", Self: "server", PrincipalKey: principalString})
	closeHost(t, host)
	if err := host.Serve(tutorialTodoStore, todoFactory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	serveViews(t, host.Serve)
	owner := mustGetRef(t, host, tutorialTodos, family(), principalOption("owner"))
	guest := mustGetRef(t, host, tutorialTodos, family(), principalOption("guest"))
	if err := readyErr(t, owner); err != nil {
		t.Fatal(err)
	}
	if err := readyErr(t, guest); err != nil {
		t.Fatal(err)
	}
	if variant := owner.Variant(); variant == nil || *variant != "editor" {
		t.Fatalf("owner variant = %v", variant)
	}
	if variant := guest.Variant(); variant == nil || *variant != "viewer" {
		t.Fatalf("guest variant = %v", variant)
	}
	if _, err := host.Root(func(ctx Ctx) (any, error) { return callMember(owner, "add", text("Buy milk"), ctx) }, RootOptions{Principal: "owner"}); err != nil {
		t.Fatal(err)
	}
	if got := jsonText(t, readable(t, guest, "rows").Value()); got != `[{"text":"Buy milk","done":false}]` {
		t.Fatalf("guest rows = %s", got)
	}
	if _, err := callMember(guest, "add", text("Not allowed"), nil); !IsChordError(err, CodeNotInVariant) {
		t.Fatalf("guest add = %v", err)
	}
	if _, err := callMember(owner, "add", text(" "), nil); !IsChordError(err, CodeInvalid) {
		t.Fatalf("owner add = %v", err)
	}
}

// tutorial.test.ts:273-303 "06: remote methods and live state over a memory link".
func TestTutorial06RemoteMethodsAndLiveStateOverAMemoryLink(t *testing.T) {
	server := CreateHost(HostOptions{Self: "server", Name: "server"})
	closeHost(t, server)
	if err := server.Serve(tutorialTodoStore, todoFactory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	serveViews(t, server.Serve)
	down, up := MemoryLinkPair()
	if err := server.Accept(down, AcceptOptions{Principal: "owner", Kind: KindClient}); err != nil {
		t.Fatal(err)
	}
	browser := CreateHost(HostOptions{Self: "browser", Name: "browser", Kind: KindClient, Upstream: &UpstreamOptions{Link: up, ConnectOptions: ConnectOptions{Principal: "server"}}})
	closeHost(t, browser)
	todos := mustGetRef(t, browser, tutorialTodos, family(), nil)
	if variant := todos.Variant(); variant == nil || *variant != "editor" {
		t.Fatalf("variant %v, want editor", variant)
	}
	rows := readable(t, todos, "rows")
	if got := jsonText(t, rows.Value()); got != "[]" {
		t.Fatalf("rows = %s", got)
	}
	updated := newSignal()
	stop := rows.Subscribe(func(value any) {
		if list, ok := value.([]any); ok && len(list) == 1 {
			updated.resolve()
		}
	})
	if _, err := browser.Root(func(ctx Ctx) (any, error) { return callMember(todos, "add", text("Buy milk"), ctx) }, RootOptions{}); err != nil {
		t.Fatal(err)
	}
	updated.wait(t)
	if got := jsonText(t, rows.Value()); got != `[{"text":"Buy milk","done":false}]` {
		t.Fatalf("rows = %s", got)
	}
	// Deliberately bypass the contract's types to simulate a malformed caller.
	if _, err := callMember(todos, "add", chordjson.ObjectOf("text", float64(123)), nil); !IsChordError(err, CodeInvalid) {
		t.Fatalf("malformed add = %v", err)
	}
	stop()
	// A held reference keeps its state without listeners.
	if got := jsonText(t, rows.Value()); got != `[{"text":"Buy milk","done":false}]` {
		t.Fatalf("rows after unsubscribe = %s", got)
	}
	todos.Release()
}

// tutorial.test.ts:305-348 "07: listeners react and tasks start fresh calls".
func TestTutorial07ListenersReactAndTasksStartFreshCalls(t *testing.T) {
	activity := Local("tutorial.activity", LocalDef{Version: 1, Input: tObject(), Members: Fields[any]{{"seen", tNumber()}}})
	host := CreateHost(HostOptions{Self: "server"})
	closeHost(t, host)
	if err := host.Serve(tutorialTodoStore, todoFactory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	observed, seeded, stopped := newSignal(), newSignal(), newSignal()
	if err := host.Serve(activity, func(_ any, _ Principal, self Self) (Implementation, error) {
		todos, err := self.Get(tutorialTodoStore, family(), nil)
		if err != nil {
			return nil, err
		}
		seen, err := self.State("seen", tNumber(), float64(0), nil)
		if err != nil {
			return nil, err
		}
		if err := self.On("rows", todos.Member("rows"), func(event any, ctx Ctx) error {
			list, _ := event.([]any)
			if err := seen.Replace(ctx, float64(len(list)), CellReplaceOptions{}); err != nil {
				return err
			}
			if len(list) == 1 {
				observed.resolve()
			}
			return nil
		}); err != nil {
			return nil, err
		}
		if err := self.Task(func(ctx Ctx) error {
			if _, err := callMember(todos, "add", text("Review the list"), ctx); err != nil {
				return err
			}
			seeded.resolve()
			<-ctx.Signal().Done()
			stopped.resolve()
			return nil
		}); err != nil {
			return nil, err
		}
		return chordjson.ObjectOf("seen", seen), nil
	}, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	ref := mustGetRef(t, host, activity, chordjson.ObjectOf(), nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	seeded.wait(t)
	observed.wait(t)
	if value := readable(t, ref, "seen").Value(); value != float64(1) {
		t.Fatalf("seen = %v", value)
	}
	ref.Release()
	stopped.wait(t)
}

type tutorialAction = func(ctx Ctx) (float64, error)

// tutorial.test.ts:350-390 "08: plugins contribute registry entries and expose callbacks".
func TestTutorial08PluginsContributeRegistryEntriesAndExposeCallbacks(t *testing.T) {
	actions := DefineRegistry[tutorialAction]("tutorial.actions")
	menu := Local("tutorial.menu", LocalDef{Version: 1, Input: tObject()})
	var click func() (any, error)
	storeFacet := Facet{Name: "todo-store", FacetDefinition: DefineFacet(func(api FacetApi) error {
		return api.Serve(tutorialTodoStore, todoFactory, ServeOptions{})
	}, DefineFacetOptions{})}
	menuFacet := Facet{Name: "todo-menu", FacetDefinition: DefineFacet(func(api FacetApi) error {
		if err := api.Serve(menu, func(_ any, _ Principal, self Self) (Implementation, error) {
			todos, err := self.Get(tutorialTodoStore, family(), nil)
			if err != nil {
				return nil, err
			}
			if err := self.Register(Registry[any](actions), []Entry{{Name: "addSample", Value: tutorialAction(func(ctx Ctx) (float64, error) {
				if _, err := callMember(todos, "add", text("Try plugins"), ctx); err != nil {
					return 0, err
				}
				return todos.Member("openCount").(Readable[any]).Value().(float64), nil
			})}}); err != nil {
				return nil, err
			}
			return chordjson.ObjectOf(), nil
		}, ServeOptions{Lifetime: LifetimeHost}); err != nil {
			return err
		}
		click = func() (any, error) {
			return api.Root(func(ctx Ctx) (any, error) {
				action, _ := api.Registry(Registry[any](actions)).Get("addSample", ctx)
				return action.(tutorialAction)(ctx)
			}, RootOptions{})
		}
		return nil
	}, DefineFacetOptions{Uses: []*Contract{tutorialTodoStore}})}
	host := CreateHost(HostOptions{Self: "server"})
	closeHost(t, host)
	if err := host.Load([]PluginRef{&menuFacet, &storeFacet}, LoadOptions{AwaitHostObjects: new(false)}); err != nil {
		t.Fatal(err)
	}
	if err := host.Ready(); err != nil {
		t.Fatal(err)
	}
	if got, err := click(); err != nil || got != float64(1) {
		t.Fatalf("click = %v, %v", got, err)
	}
	if size := host.Registry(Registry[any](actions)).Table(nil).Len(); size != 1 {
		t.Fatalf("table size = %d", size)
	}
}

// tutorialFacet is tutorial.test.ts:397-404 facet(label): todoFactory with its label replaced.
func tutorialFacet(label string) Facet {
	return Facet{Name: "todos", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(tutorialTodoStore, func(input any, principal Principal, self Self) (Implementation, error) {
			implementation, err := todoFactory(input, principal, self)
			if err != nil {
				return nil, err
			}
			implementation.Set("label", methodFunc(func(any, Ctx) (any, error) { return label, nil }))
			return implementation, nil
		}, ServeOptions{})
	}}}
}

// tutorial.test.ts:392-481 "09: compatible reload, migration and an old-contract adapter".
func TestTutorial09CompatibleReloadMigrationAndAnOldContractAdapter(t *testing.T) {
	host := CreateHost(HostOptions{Self: "server"})
	closeHost(t, host)
	original := tutorialFacet("Original")
	if err := host.Load([]PluginRef{&original}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	todos := mustGetRef(t, host, tutorialTodoStore, family(), nil)
	if _, err := callMember(todos, "add", text("Buy milk"), nil); err != nil {
		t.Fatal(err)
	}
	entered, resume := newSignal(), newSignal()
	type result struct {
		value any
		err   error
	}
	oldRoot := make(chan result, 1)
	go func() {
		value, err := host.Root(func(ctx Ctx) (any, error) {
			first, err := callMember(todos, "label", nil, ctx)
			if err != nil {
				return nil, err
			}
			entered.resolve()
			<-resume.ch // Test-only pause: pin an old caller during reload.
			second, err := callMember(todos, "label", nil, ctx)
			return []any{first, second}, err
		}, RootOptions{})
		oldRoot <- result{value, err}
	}()
	entered.wait(t)
	updated := tutorialFacet("Updated")
	compatible, err := host.Reload([]PluginRef{&updated}, ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(compatible.Gated) != 0 {
		t.Fatalf("gated = %v", compatible.Gated)
	}
	if got, err := callMember(todos, "label", nil, nil); err != nil || got != "Updated" {
		t.Fatalf("label = %v, %v", got, err)
	}
	resume.resolve()
	if got := <-oldRoot; got.err != nil || jsonText(t, got.value) != `["Original","Original"]` {
		t.Fatalf("old root = %v, %v", got.value, got.err)
	}
	rows := readable(t, todos, "rows")
	if got := field(rows.Value().([]any)[0], "text"); got != "Buy milk" {
		t.Fatalf("rows[0].text = %v", got)
	}

	var eventsMu sync.Mutex
	var events []string
	stopEvents := rows.Events(func(event StateEvent[any]) {
		eventsMu.Lock()
		events = append(events, string(event.Type))
		eventsMu.Unlock()
	})
	migrating := Facet{Name: "todos", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(tutorialTodoStore, todoStore(&StateOptions{
			Version: 2,
			Migrate: func(old any, from int) (any, error) {
				if from != 1 {
					return nil, errors.New("migrate from version " + jsonText(t, from))
				}
				todos := old.([]any)
				out := make([]any, len(todos))
				for index, todo := range todos {
					out[index] = delta.JsonObjectOf("text", strings.ToUpper(field(todo, "text").(string)), "done", field(todo, "done"))
				}
				return out, nil
			},
		}), ServeOptions{})
	}}}
	migrated, err := host.Reload([]PluginRef{&migrating}, ReloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(migrated.Gated) != 1 {
		t.Fatalf("gated = %v", migrated.Gated)
	}
	if got := jsonText(t, rows.Value()); got != `[{"text":"BUY MILK","done":false}]` {
		t.Fatalf("rows = %s", got)
	}
	eventsMu.Lock()
	if !slices.Equal(events, []string{"snap", "reset"}) {
		t.Fatalf("events = %v", events)
	}
	eventsMu.Unlock()
	stopEvents()

	// Remove label from the new provider's contract: it is now called heading.
	var members Fields[any]
	for _, member := range tutorialTodoStore.Local.Members {
		if member.Name != "label" {
			members = append(members, member)
		}
	}
	members = append(members, Field[any]{"heading", Method(nil, tString())})
	renamedStore := Local("tutorial.todos", LocalDef{Version: 2, Input: tutorialTodoStore.Local.Input, Members: members})
	renaming := Facet{Name: "todos", FacetDefinition: FacetDefinition{Setup: func(api FacetApi) error {
		return api.Serve(renamedStore, func(input any, principal Principal, self Self) (Implementation, error) {
			implementation, err := todoStore(&StateOptions{Version: 2})(input, principal, self)
			if err != nil {
				return nil, err
			}
			label := implementation.Value("label")
			renamed := chordjson.NewObject(implementation.Len())
			for key, value := range implementation.All() {
				if key != "label" {
					renamed.Set(key, value)
				}
			}
			renamed.Set("heading", label)
			return renamed, nil
		}, ServeOptions{
			Accepts: Accepts{1: func(current Implementation) Implementation {
				adapted := current.Clone()
				adapted.Set("label", current.Value("heading"))
				return adapted
			}},
		})
	}}}
	if _, err := host.Reload([]PluginRef{&renaming}, ReloadOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, err := callMember(todos, "label", nil, nil); err != nil || got != "Todos for family" {
		t.Fatalf("label = %v, %v", got, err)
	}
	newer := mustGetRef(t, host, renamedStore, family(), nil)
	if got, err := callMember(newer, "heading", nil, nil); err != nil || got != "Todos for family" {
		t.Fatalf("heading = %v, %v", got, err)
	}
	if got := field(rows.Value().([]any)[0], "text"); got != "BUY MILK" {
		t.Fatalf("rows[0].text = %v", got)
	}
}

// tutorial.test.ts:483-510 "10: stub a provider with a fake facet and check error codes".
func TestTutorial10StubAProviderWithAFakeFacetAndCheckErrorCodes(t *testing.T) {
	host := CreateHost(HostOptions{Name: "server", Self: "server", Dev: true})
	closeHost(t, host)
	var mu sync.Mutex
	var received []string
	fake := Facet{Name: "fake-store", FacetDefinition: DefineFacet(func(api FacetApi) error {
		return api.Serve(tutorialTodoStore, func(input any, principal Principal, self Self) (Implementation, error) {
			implementation, err := todoFactory(input, principal, self)
			if err != nil {
				return nil, err
			}
			implementation.Set("add", methodFunc(func(args any, _ Ctx) (any, error) {
				mu.Lock()
				received = append(received, field(args, "text").(string))
				mu.Unlock()
				return nil, nil
			}))
			return implementation, nil
		}, ServeOptions{})
	}, DefineFacetOptions{})}
	if err := host.Load([]PluginRef{&fake}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	serveViews(t, host.Serve)
	owner := mustGetRef(t, host, tutorialTodos, family(), principalOption("owner"))
	if _, err := callMember(owner, "add", text("Test delegation"), nil); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if !slices.Equal(received, []string{"Test delegation"}) {
		t.Fatalf("received = %v", received)
	}
	mu.Unlock()
	if _, err := callMember(owner, "add", text(""), nil); !IsChordError(err, CodeInvalid) {
		t.Fatalf("empty add = %v", err)
	}
	owner.Release()
	if _, err := callMember(owner, "add", text("Too late"), nil); !IsChordError(err, CodeReleased) {
		t.Fatalf("released add = %v", err)
	}
}

// tutorial.test.ts:512-526 "11: short roots release temporary holds".
func TestTutorial11ShortRootsReleaseTemporaryRefs(t *testing.T) {
	host := CreateHost(HostOptions{Self: "server"})
	closeHost(t, host)
	if err := host.Serve(tutorialTodoStore, todoFactory, ServeOptions{}); err != nil {
		t.Fatal(err)
	}
	screen := mustGetRef(t, host, tutorialTodoStore, family(), nil)
	if err := readyErr(t, screen); err != nil {
		t.Fatal(err)
	}
	if _, err := host.Root(func(ctx Ctx) (any, error) {
		temporary, err := ctx.Get(tutorialTodoStore, family(), nil)
		if err != nil {
			return nil, err
		}
		return callMember(temporary, "add", text("Keep roots short"), ctx)
	}, RootOptions{}); err != nil { // The temporary reference is released here; screen still holds the store.
		t.Fatal(err)
	}
	if got := readable(t, screen, "openCount").Value(); got != float64(1) {
		t.Fatalf("openCount = %v", got)
	}
	screen.Release() // No reference remains; without persistence, disposal loses these rows.
}
