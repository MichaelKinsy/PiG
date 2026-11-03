// Ports the cases of packages/durable/test/harness-registry-reader.test.ts that run on a Chord host (Pi e7a0df27, 5f312dac):
// the registry reader of a loaded host's composed registry, and Durable's views followed from another object. The
// registries.RegistryValue stand-in of registry_reader_test.go keeps covering registryReader's own logic.

package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	chordhost "github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func rrMust[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}

var (
	// rrExtensions is p3's pattern: the extensions registry composes Durable's snapshot, leaving out an extension Durable
	// refuses.
	rrExtensions = rrMust(registries.DefineRegistry[any, any]("app.harness.extensions", registries.DefineRegistryOptions{Version: 1}))
	// rrPlugin is a host-lifetime object contributing an extension it replaces with `install` (one with the task `app.done`).
	rrPlugin = rrMust(contracts.Local("app.plugin", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("install", contracts.NewMethod())},
	}))
	// rrHarnesses is p3's pattern for Durable's views from other objects: the object that opens Durable contributes its Harness.
	rrHarnesses = rrMust(registries.DefineRegistry[any, any]("app.harnesses", registries.DefineRegistryOptions{Version: 1}))
	// rrDurable opens a Harness (a new one per attempt), contributes it, and follows its task graph: its Session closing fails it.
	rrDurable = rrMust(contracts.Local("app.durable", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{
			contracts.Mem("close", contracts.NewMethod()),
			contracts.Mem("write", contracts.NewMethod(contracts.Str())),
		},
	}))
	// rrViewer follows the root conversation's entry kinds through the contributed Harness, from another object.
	rrViewer = rrMust(contracts.Local("app.viewer", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("kinds", contracts.Arr(contracts.Str()))},
	}))
)

// rrDoneTask is Pi's `Done`: a task of the extension a plugin installs later, which completes at once.
func rrDoneTask() durable.Task[durable.JsonValue, blockedState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[durable.JsonValue, blockedState, durable.JsonValue, any]{
		Name: "app.done", Version: 1,
		Initial: func(durable.JsonValue) blockedState { return blockedState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[durable.JsonValue, blockedState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ blockedRecord, runtime blockedRuntime) error {
				return runtime.Commit(ctx, func(durable.Tx, blockedRecord) (*durable.NextTaskState[blockedState, durable.JsonValue], error) {
					return completed[blockedState, durable.JsonValue](nil), nil
				})
			},
		},
		Abort: func(context.Context, blockedRecord, blockedRuntime) error { return nil },
	})
}

// rrServe is Pi's `serve`: the facet that serves the extensions registry (`compose` installs each contributed extension in a
// Durable registry, reporting the one Durable refuses) and the plugin that contributes an extension and replaces it.
func rrServe(done durable.AnyTask) func(chordhost.FacetHost, chordhost.FacetSelf) error {
	return func(host chordhost.FacetHost, _ chordhost.FacetSelf) error {
		err := host.Serve(rrExtensions, registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], composeContext registries.ComposeContext) (any, error) {
				registry := CreateRegistry()
				for _, entry := range table {
					if err := registry.Install(entry.Value.(*durable.Extension)); err != nil {
						composeContext.Report(registries.RegistryConflict{Message: err.Error(), Plugins: []string{entry.Plugin}})
					}
				}
				return registry.Snapshot(), nil
			},
		}, chordhost.ServeOptions{})
		if err != nil {
			return err
		}
		return host.Serve(rrPlugin, func(_ *delta.JsonObject, self chordhost.Self) (contracts.Implementation, error) {
			extension := self.Writable(&durable.Extension{Name: "app.plugin"})
			self.(chordhost.StoreSelf).Contribute(rrExtensions, extension)
			return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
				"install": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					return nil, extension.Set(&durable.Extension{Name: "app.plugin", Tasks: []durable.AnyTask{done}})
				},
			}}}, nil
		}, chordhost.ServeOptions{})
	}
}

// rrOpenHost is `createHost({ onError })` loaded with one facet.
func rrOpenHost(t *testing.T, setup func(chordhost.FacetHost, chordhost.FacetSelf) error) chordhost.Host {
	t.Helper()
	host := rrCreateHost(t)
	rrLoad(t, host, setup)
	return host
}

func rrCreateHost(t *testing.T) chordhost.Host {
	t.Helper()
	host, err := chordhost.CreateHost(chordhost.HostOptions{OnError: func(chordhost.HostError) {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rrBounded(t, "closing the host", func() { _ = host.Close(testContext) }) })
	return host
}

func rrLoad(t *testing.T, host chordhost.Host, setup func(chordhost.FacetHost, chordhost.FacetSelf) error) {
	t.Helper()
	report, err := host.Load([]chordhost.HostFacet{{Name: "@app/core", Facet: chordhost.DefineFacet(setup)}}, testContext)
	if err != nil {
		t.Fatal(err)
	}
	for _, failed := range report.Failed {
		t.Fatalf("plugin %q was left out: %v", failed.Plugin, failed.Error)
	}
}

func rrReference(t *testing.T, host chordhost.Host, typ *contracts.Contract) *chordhost.Reference {
	t.Helper()
	reference, err := host.Get(testContext, typ, delta.JsonObjectOf(), chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

func rrHeld(t *testing.T, host chordhost.Host, typ *contracts.Contract) *chordhost.Reference {
	t.Helper()
	reference, err := host.Hold(testContext, typ, delta.JsonObjectOf(), chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return reference
}

// rrBounded runs fn on its own goroutine and fails the test with what it panicked with, or when it has not returned in time:
// a panic that leaves a Chord lock held would otherwise hang the test and its cleanup to the package timeout.
func rrBounded(t *testing.T, what string, fn func()) {
	t.Helper()
	finished := make(chan any, 1)
	go func() {
		defer func() { finished <- recover() }()
		fn()
	}()
	select {
	case recovered := <-finished:
		if recovered != nil {
			t.Errorf("%s panicked: %v", what, recovered)
		}
	case <-time.After(10 * time.Second):
		t.Errorf("%s did not return in 10 s", what)
	}
}

func rrWait(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// rrRecorder is an observer that records nothing, which a listener of the registry's value attaches to see whether a Chord
// call is allowed inside the batch that composed.
type rrRecorder struct{}

func (rrRecorder) Advance(chord.NativeWatchCall[durable.RegistrySnapshot], context.Context) {}
func (rrRecorder) End(chord.WatchEnd)                                                       {}

func TestRegistryReaderOnAChordHost(t *testing.T) {
	t.Run("reads the composed snapshot; a listener is called synchronously, inside the batch that composed", func(t *testing.T) {
		done := rrDoneTask()
		host := rrOpenHost(t, rrServe(done.Erased()))
		source, err := host.Registry(testContext, rrExtensions, chordhost.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		value := registries.NewRegistryValue[durable.RegistrySnapshot](source)
		reader := NewRegistryReader(testContext, value)
		if extension := reader.Snapshot().Extension("app.plugin"); extension == nil || len(extension.Tasks) != 0 {
			t.Fatalf("the snapshot before the install holds %+v, want the plugin without tasks", extension)
		}
		var mu sync.Mutex
		var calls []string
		unsubscribe := reader.Subscribe(func() {
			// Inside Chord's batch: a Chord call here is refused, so a listener only wakes its scheduler.
			outcome := "outside"
			if _, err := value.Observe(rrRecorder{}, testContext); err != nil {
				outcome = "other"
				var chordErr *data.ChordError
				if errors.As(err, &chordErr) && chordErr.Reason != "" {
					outcome = string(chordErr.Reason)
				}
			}
			mu.Lock()
			calls = append(calls, outcome)
			mu.Unlock()
		})
		plugin := rrReference(t, host, rrPlugin)
		if _, err := plugin.Call("install", nil, testContext); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		got := append([]string(nil), calls...)
		mu.Unlock()
		if len(got) != 1 || got[0] != "reentrant" {
			t.Fatalf("calls = %v, want [reentrant]", got)
		}
		// Pi: toBe(Done), the definition itself.
		if task := reader.Snapshot().Task("app.done"); task.Definition == nil || task.Definition != done.Erased().Definition {
			t.Fatalf("the snapshot after the install holds %+v, want the installed app.done definition", task)
		}
		rrBounded(t, "unsubscribe", unsubscribe)
	})

	t.Run("wakes a Harness's scheduler: a task blocked on its missing definition runs once a contribution brings it", func(t *testing.T) {
		done := rrDoneTask()
		host := rrOpenHost(t, rrServe(done.Erased()))
		source, err := host.Registry(testContext, rrExtensions, chordhost.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := OpenHarness(testContext, storage.NewMemoryStorage(), HarnessOptions{
			Models: ai.CreateModels(), Registry: NewRegistryReader(testContext, registries.NewRegistryValue[durable.RegistrySnapshot](source)),
		})
		if err != nil {
			t.Fatal(err)
		}
		opened.Resume()
		id := tkStart(t, mustRoot(t, opened, nil), done.Erased())
		type waitResult struct {
			receipt durable.SettledTask[durable.JsonValue]
			err     error
		}
		waited := make(chan waitResult, 1)
		go func() {
			receipt, err := opened.WaitForTask(testContext, id)
			waited <- waitResult{receipt, err}
		}()
		select {
		case <-waited:
			t.Fatal("the task ran before its definition was installed")
		case <-time.After(10 * time.Millisecond):
		}
		if _, err := rrReference(t, host, rrPlugin).Call("install", nil, testContext); err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-waited:
			if result.err != nil {
				t.Fatal(result.err)
			}
			if outcome := tkOutcome(t, result.receipt); outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome %+v, want completed", outcome)
			}
		case <-time.After(10 * time.Second):
			closeHarness(t, opened)
			t.Fatal("the task did not run after its definition was installed: the registry reader did not wake the scheduler")
		}
		closeHarness(t, opened)
	})
}

// A reader made with a context that is already done: Chord ends its observer before Observe returns, so the reader reads the
// value itself (Pi's `value.value`) rather than serving the composition it started at.
func TestRegistryReaderWithACancelledContextReadsTheValueItself(t *testing.T) {
	done := rrDoneTask()
	host := rrOpenHost(t, rrServe(done.Erased()))
	source, err := host.Registry(testContext, rrExtensions, chordhost.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	value := registries.NewRegistryValue[durable.RegistrySnapshot](source)
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	reader := NewRegistryReader(ctx, value)
	if _, err := rrReference(t, host, rrPlugin).Call("install", nil, testContext); err != nil {
		t.Fatal(err)
	}
	if task := reader.Snapshot().Task("app.done"); task.Definition == nil {
		t.Fatal("a reader made with a cancelled ctx serves its start composition: the snapshot after the install has no app.done task")
	}
}

// rrGraphFollower and rrViewFollower hand a Harness's observer calls to the host's followed source: Chord's watch shapes, the
// value as JSON.
type rrGraphFollower struct {
	observer chordhost.FollowObserver[any]
}

func (follower rrGraphFollower) Advance(ctx context.Context, call chord.WatchCall[TaskGraph]) {
	forwardFollowed(follower.observer, ctx, call)
}
func (follower rrGraphFollower) End(end chord.WatchEnd) { follower.observer.End(end) }

type rrViewFollower struct {
	observer chordhost.FollowObserver[any]
}

func (follower rrViewFollower) Advance(ctx context.Context, call chord.WatchCall[ConversationView]) {
	forwardFollowed(follower.observer, ctx, call)
}
func (follower rrViewFollower) End(end chord.WatchEnd) { follower.observer.End(end) }

func forwardFollowed[T any](observer chordhost.FollowObserver[any], ctx context.Context, call chord.WatchCall[T]) {
	value, err := durable.ToJsonValue(call.Value)
	if err != nil {
		panic(err)
	}
	observer.Advance(chord.WatchCall[any]{Kind: call.Kind, Value: value, Ops: call.Ops, Retired: call.Retired, Error: call.Error}, ctx)
}

func rrServeViews(host chordhost.FacetHost, _ chordhost.FacetSelf) error {
	err := host.Serve(rrHarnesses, registries.RegistryProvider[any, any]{
		Compose: func(table registries.RegistryTable[any], _ registries.ComposeContext) (any, error) {
			if len(table) == 0 {
				return nil, nil
			}
			opened, err := table[len(table)-1].Value.(chordhost.Native[any]).Value()
			if err != nil {
				return nil, nil
			}
			return opened, nil
		},
	}, chordhost.ServeOptions{})
	if err != nil {
		return err
	}
	err = host.Serve(rrDurable, func(_ *delta.JsonObject, self chordhost.Self) (contracts.Implementation, error) {
		harness := self.Open(
			func(ctx context.Context) (any, error) {
				opened, err := OpenHarness(ctx, storage.NewMemoryStorage(), HarnessOptions{Models: ai.CreateModels(), Registry: CreateRegistry()})
				if err != nil {
					return nil, err
				}
				if _, err := opened.Root(ctx, nil); err != nil {
					_ = opened.Close(ctx)
					return nil, err
				}
				return opened, nil
			},
			func(value any, _ chordhost.NativeCloseReason, ctx context.Context) error {
				return value.(Harness).Close(ctx)
			},
		)
		self.(chordhost.StoreSelf).Contribute(rrHarnesses, harness)
		self.Follow(func(observer chordhost.FollowObserver[any], ctx context.Context) (*chordhost.FollowAttachment[delta.JsonValue], error) {
			opened, err := harness.Value()
			if err != nil {
				return nil, err
			}
			observation, err := opened.(Harness).ObserveTaskGraph(ctx, rrGraphFollower{observer})
			if err != nil {
				return nil, err
			}
			value, err := durable.ToJsonValue(observation.Value())
			if err != nil {
				return nil, err
			}
			return &chordhost.FollowAttachment[delta.JsonValue]{Value: value, Detach: observation.Detach}, nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"close": func(_ delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				// The handler waits for the Harness, whose Session close ends the followers of its task graph: it gives the
				// section back first (Pi's `await`), so that end enters one.
				chordctx.Suspend(ctx)
				opened, err := harness.Value()
				if err != nil {
					return nil, err
				}
				return nil, opened.(Harness).Close(ctx)
			},
			"write": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
				chordctx.Suspend(ctx)
				opened, err := harness.Value()
				if err != nil {
					return nil, err
				}
				root, err := opened.(Harness).Conversation(ctx, durable.ROOT_CONVERSATION_ID)
				if err != nil {
					return nil, err
				}
				_, err = root.Submit(ctx, durable.SubmissionDraft{Type: durable.SubmissionTypeWrite, Entry: &durable.EntryDraft{Kind: args.(string)}})
				return nil, err
			},
		}}}, nil
	}, chordhost.ServeOptions{})
	if err != nil {
		return err
	}
	return host.Serve(rrViewer, func(_ *delta.JsonObject, self chordhost.Self) (contracts.Implementation, error) {
		harness := self.Registry(rrHarnesses)
		self.RetryOn(harness)
		view := self.Follow(func(observer chordhost.FollowObserver[any], ctx context.Context) (*chordhost.FollowAttachment[delta.JsonValue], error) {
			opened, _ := harness.Value().(Harness)
			if opened == nil {
				return nil, nil
			}
			observation, err := opened.ObserveConversation(ctx, durable.ROOT_CONVERSATION_ID, rrViewFollower{observer}, nil)
			if err != nil {
				return nil, err
			}
			value, err := durable.ToJsonValue(observation.Value())
			if err != nil {
				return nil, err
			}
			return &chordhost.FollowAttachment[delta.JsonValue]{Value: value, Detach: observation.Detach}, nil
		})
		kinds := self.Derive([]any{view}, func(values []any) (delta.JsonValue, error) {
			entries, _ := values[0].(*delta.JsonObject).Value("entries").([]any)
			out := make([]any, 0, len(entries))
			for _, entry := range entries {
				out = append(out, entry.(*delta.JsonObject).Value("kind"))
			}
			return out, nil
		})
		return contracts.Implementation{Members: contracts.VariantImplementation{State: map[string]chord.Watchable[delta.JsonValue]{"kinds": kinds}}}, nil
	}, chordhost.ServeOptions{})
}

// rrKinds is `viewer.kinds.value`.
func rrKinds(t *testing.T, viewer *chordhost.Reference) []string {
	t.Helper()
	value, err := viewer.Member("kinds").Value()
	if err != nil {
		return nil
	}
	items, _ := value.([]any)
	kinds := make([]string, 0, len(items))
	for _, item := range items {
		kinds = append(kinds, item.(string))
	}
	return kinds
}

func rrHasKind(t *testing.T, viewer *chordhost.Reference, kind string) bool {
	t.Helper()
	return slices.Contains(rrKinds(t, viewer), kind)
}

func TestDurablesViewsFromOtherObjects(t *testing.T) {
	// slice 8 §5, S1.
	t.Run("a follower of a contributed Harness fails when that Harness's Session closes, and follows the next one", func(t *testing.T) {
		host := rrOpenHost(t, rrServeViews)
		durableRef := rrHeld(t, host, rrDurable)
		viewer := rrHeld(t, host, rrViewer)
		rrWait(t, "the viewer is live", func() bool { return viewer.State().Value().Kind == chordhost.RefStateLive })
		if _, err := durableRef.Call("write", "app.first", testContext); err != nil {
			t.Fatal(err)
		}
		rrWait(t, "the viewer shows app.first", func() bool { return rrHasKind(t, viewer, "app.first") })
		// The Session closes: both followers fail `transient` (S1); a retry opens the next Harness, whose contribution
		// wakes the viewer (`retryOn`), which follows it.
		// The call is cut off by its object's failure: the Session's close fails the object that opened it.
		if _, err := durableRef.Call("close", nil, testContext); err == nil || !strings.Contains(err.Error(), "session closed") {
			t.Fatalf("close returned %v, want an error that says session closed", err)
		}
		rrWait(t, "the viewer fails transient", func() bool {
			state := viewer.State().Value()
			return state.Kind == chordhost.RefStateFailed && state.Cause.Kind == data.FailureTransient
		})
		durableRef.Retry()
		rrWait(t, "the viewer is live on the next Harness", func() bool { return viewer.State().Value().Kind == chordhost.RefStateLive })
		if rrHasKind(t, viewer, "app.first") {
			t.Fatalf("the next Harness shows %v, want it without app.first", rrKinds(t, viewer))
		}
		if _, err := durableRef.Call("write", "app.second", testContext); err != nil {
			t.Fatal(err)
		}
		rrWait(t, "the viewer shows app.second", func() bool { return rrHasKind(t, viewer, "app.second") })
	})
}
