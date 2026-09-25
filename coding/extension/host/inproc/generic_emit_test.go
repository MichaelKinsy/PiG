package inproc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithGenericHandler registers a typed handler for the given event
// type on a fresh fake extension. The handler returns whatever `fn`
// returns (or nil to short-circuit silently).
func extWithGenericHandler(path, eventType string, fn func(any, context.Context) any) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers[eventType] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ctx, _ := args[1].(context.Context)
			return fn(args[0], ctx), nil
		},
	}
	return ext
}

// ─── Emit: type-extraction & nil handling ─────────────────────────────────

func TestEmit_NilEventReturnsNilNil(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.Emit(context.Background(), nil)
	if err != nil || got != nil {
		t.Errorf("got=(%v, %v), want (nil, nil)", got, err)
	}
}

// TestEmit_EventWithoutTypeFieldReturnsError: defensive: any value
// passed to Emit must be a struct with a non-empty Type string field.
func TestEmit_EventWithoutTypeFieldReturnsError(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	_, err := r.Emit(context.Background(), 42)
	if err == nil {
		t.Errorf("Emit(42) err = nil, want error")
	}
}

func TestEmit_EventWithEmptyTypeReturnsError(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	_, err := r.Emit(context.Background(), extension.SessionStartEvent{Type: ""})
	if err == nil {
		t.Errorf("Emit(empty Type) err = nil, want error")
	}
}

// ─── Emit: dispatch order, no-handlers ────────────────────────────────────

func TestEmit_DispatchUsesEventTypeAsHandlerKey(t *testing.T) {
	var wrongFired, rightFired atomic.Int32
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_start", func(any, context.Context) any {
			rightFired.Add(1)
			return nil
		}),
		extWithGenericHandler("/ext/b", "session_shutdown", func(any, context.Context) any {
			wrongFired.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	_, _ = r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})

	if rightFired.Load() != 1 {
		t.Errorf("session_start handler fired %d, want 1", rightFired.Load())
	}
	if wrongFired.Load() != 0 {
		t.Errorf("session_shutdown handler fired %d, want 0 (different event type)", wrongFired.Load())
	}
}

func TestEmit_DispatchOrderMatchesLoadOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex
	mk := func(tag string) extension.Extension {
		return extWithGenericHandler("/ext/"+tag, "session_start", func(any, context.Context) any {
			mu.Lock()
			order = append(order, tag)
			mu.Unlock()
			return nil
		})
	}
	r := inproc.NewRunner([]extension.Extension{mk("a"), mk("b"), mk("c")}, ".")
	_, _ = r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "a" || order[1] != "b" || order[2] != "c" {
		t.Errorf("dispatch order = %v, want [a b c]", order)
	}
}

// ─── Emit: SessionBefore* short-circuit ───────────────────────────────────

// TestEmit_SessionBeforeForkCancelShortCircuits: when a handler returns
// {Cancel: true}, dispatch halts and that result is returned. Subsequent
// handlers do not run.
//
// upstream: runner.ts:685-690 (`if (result.cancel) return result;`)
func TestEmit_SessionBeforeForkCancelShortCircuits(t *testing.T) {
	var thirdFired atomic.Int32
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_before_fork", func(any, context.Context) any {
			return &extension.SessionBeforeForkResult{Cancel: false}
		}),
		extWithGenericHandler("/ext/b", "session_before_fork", func(any, context.Context) any {
			return &extension.SessionBeforeForkResult{Cancel: true, SkipConversationRestore: true}
		}),
		extWithGenericHandler("/ext/c", "session_before_fork", func(any, context.Context) any {
			thirdFired.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.Emit(context.Background(), extension.SessionBeforeForkEvent{Type: "session_before_fork"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	res, ok := got.(*extension.SessionBeforeForkResult)
	if !ok {
		t.Fatalf("got = %T, want *SessionBeforeForkResult", got)
	}
	if !res.Cancel {
		t.Errorf("Cancel = false, want true")
	}
	if !res.SkipConversationRestore {
		t.Errorf("SkipConversationRestore = false, want true")
	}
	if thirdFired.Load() != 0 {
		t.Errorf("third handler fired %d, want 0 (cancel short-circuits)", thirdFired.Load())
	}
}

// TestEmit_SessionBeforeWithoutCancelKeepsLastResult: when no handler
// returns Cancel=true, the LAST non-nil result is returned (mirrors
// upstream `result = handlerResult`; only Cancel returns early).
func TestEmit_SessionBeforeWithoutCancelKeepsLastResult(t *testing.T) {
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_before_compact", func(any, context.Context) any {
			return &extension.SessionBeforeCompactResult{Cancel: false}
		}),
		extWithGenericHandler("/ext/b", "session_before_compact", func(any, context.Context) any {
			return &extension.SessionBeforeCompactResult{Cancel: false}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.Emit(context.Background(), extension.SessionBeforeCompactEvent{Type: "session_before_compact"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(*extension.SessionBeforeCompactResult); !ok {
		t.Errorf("got = %T, want *SessionBeforeCompactResult (last non-nil)", got)
	}
}

// TestEmit_NonSessionBeforeReturnsNilEvenIfHandlerReturnsValue: for
// events that aren't SessionBefore*, handler results are discarded.
// This locks upstream's contract that only SessionBefore* events have
// a meaningful result type.
func TestEmit_NonSessionBeforeReturnsNilEvenIfHandlerReturnsValue(t *testing.T) {
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_start", func(any, context.Context) any {
			return "irrelevant"
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got = %v, want nil (non-SessionBefore* discards results)", got)
	}
}

// ─── Emit: error routing ──────────────────────────────────────────────────

func TestEmit_HandlerErrorRoutesViaEmitErrorAndContinues(t *testing.T) {
	var subsequentRan atomic.Int32
	exts := []extension.Extension{
		{
			Path: "/ext/bad",
			Handlers: map[string][]extension.HandlerFn{
				"session_start": {func(args ...any) (any, error) { return nil, errors.New("kaboom") }},
			},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithGenericHandler("/ext/good", "session_start", func(any, context.Context) any {
			subsequentRan.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured atomic.Int32
	r.AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "session_start" && e.ExtensionPath == "/ext/bad" {
			captured.Add(1)
		}
	})

	if _, err := r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if captured.Load() != 1 {
		t.Errorf("error listener captured %d, want 1", captured.Load())
	}
	if subsequentRan.Load() != 1 {
		t.Errorf("subsequent handler fired %d, want 1 (chain must continue past error)", subsequentRan.Load())
	}
}

// TestEmit_HandlerPanicIsolatedAndContinues: a handler that panics must not
// crash the host. The panic is recovered, routed to the error listener like a
// returned error, and the dispatch chain continues. Mirrors upstream
// runner.ts which wraps every handler in try/catch (runner.ts:702-714).
// Without recovery a misbehaving extension panicking during e.g.
// session_compact tears down the TUI and leaves the terminal in raw mode.
func TestEmit_HandlerPanicIsolatedAndContinues(t *testing.T) {
	var subsequentRan atomic.Int32
	exts := []extension.Extension{
		{
			Path: "/ext/panic",
			Handlers: map[string][]extension.HandlerFn{
				"session_compact": {func(args ...any) (any, error) { panic("boom") }},
			},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithGenericHandler("/ext/good", "session_compact", func(any, context.Context) any {
			subsequentRan.Add(1)
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured atomic.Int32
	r.AddErrorListener(func(e *extension.ExtensionError) {
		if e.Event == "session_compact" && e.ExtensionPath == "/ext/panic" {
			captured.Add(1)
		}
	})

	// Must not panic out of Emit.
	if _, err := r.Emit(context.Background(), extension.SessionCompactEvent{Type: "session_compact"}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if captured.Load() != 1 {
		t.Errorf("error listener captured %d, want 1 (panic must route via emitError)", captured.Load())
	}
	if subsequentRan.Load() != 1 {
		t.Errorf("subsequent handler fired %d, want 1 (chain must continue past panic)", subsequentRan.Load())
	}
}

// TestEmit_HandlerCanReadExtensionContext: parity with EmitToolCall -
// the dispatch attaches an extension.Context to the context.Context.
func TestEmit_HandlerCanReadExtensionContext(t *testing.T) {
	var saw atomic.Bool
	exts := []extension.Extension{
		extWithGenericHandler("/ext/a", "session_start", func(_ any, ctx context.Context) any {
			if extension.FromContext(ctx) != nil {
				saw.Store(true)
			}
			return nil
		}),
	}
	r := inproc.NewRunner(exts, ".")
	_, _ = r.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"})
	if !saw.Load() {
		t.Errorf("handler did not see extension.Context attached")
	}
}

// TestEmit_AllSessionBeforeVariantsRecognized: all 4 SessionBefore*
// event types short-circuit on Cancel. Locks the closed set against
// drift (if upstream adds a 5th SessionBefore* event, this test
// surfaces the gap by failing on the new one).
func TestEmit_AllSessionBeforeVariantsRecognized(t *testing.T) {
	cases := []struct {
		name      string
		event     any
		eventType string
		result    any
	}{
		{
			"session_before_switch",
			extension.SessionBeforeSwitchEvent{Type: "session_before_switch"},
			"session_before_switch",
			&extension.SessionBeforeSwitchResult{Cancel: true},
		},
		{
			"session_before_fork",
			extension.SessionBeforeForkEvent{Type: "session_before_fork"},
			"session_before_fork",
			&extension.SessionBeforeForkResult{Cancel: true},
		},
		{
			"session_before_compact",
			extension.SessionBeforeCompactEvent{Type: "session_before_compact"},
			"session_before_compact",
			&extension.SessionBeforeCompactResult{Cancel: true},
		},
		{
			"session_before_tree",
			extension.SessionBeforeTreeEvent{Type: "session_before_tree"},
			"session_before_tree",
			&extension.SessionBeforeTreeResult{Cancel: true},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var thirdFired atomic.Int32
			result := tc.result
			exts := []extension.Extension{
				extWithGenericHandler("/ext/a", tc.eventType, func(any, context.Context) any { return result }),
				extWithGenericHandler("/ext/b", tc.eventType, func(any, context.Context) any {
					thirdFired.Add(1)
					return nil
				}),
			}
			r := inproc.NewRunner(exts, ".")
			got, err := r.Emit(context.Background(), tc.event)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got == nil {
				t.Errorf("got = nil, want Cancel result for %s", tc.name)
			}
			if thirdFired.Load() != 0 {
				t.Errorf("subsequent handler fired %d, want 0 (Cancel must short-circuit)", thirdFired.Load())
			}
		})
	}
}
