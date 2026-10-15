package extensionconformance

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// bridgeResult is bridgeVoid for handlers that return a result: the runner receives the typed result as the handler's value.
func bridgeResult[E, R any](handlers []func(context.Context, E) (R, error)) []extension.HandlerFn {
	out := make([]extension.HandlerFn, 0, len(handlers))
	for _, handler := range handlers {
		out = append(out, func(args ...any) (any, error) {
			return handler(args[1].(context.Context), args[0].(E))
		})
	}
	return out
}

// TestConformanceAPIOnSessionStartAndTurnEndReachTheTypedHandlers is the Go reference for pi.on("session_start") and pi.on("turn_end") (extensions/types.ts:1563 and :1613): the handler registered through extension.API.OnSessionStart or OnTurnEnd receives the host's event unchanged.
//
// mutation-checked: the mutant "Runner.Emit passes a nil event to the handler" fails it.
func TestConformanceAPIOnSessionStartAndTurnEndReachTheTypedHandlers(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	start := extension.SessionStartEvent{Type: "session_start", Reason: "startup"}
	turn := extension.TurnEndEvent{Type: "turn_end", TurnIndex: 3}
	var gotStart extension.SessionStartEvent
	var gotTurn extension.TurnEndEvent
	api.OnSessionStart(func(_ context.Context, evt extension.SessionStartEvent) error { gotStart = evt; return nil })
	api.OnTurnEnd(func(_ context.Context, evt extension.TurnEndEvent) (extension.TurnEndEventResult, error) {
		gotTurn = evt
		return extension.TurnEndEventResult{}, nil
	})
	ext := extension.Extension{Path: "api-reference", Handlers: map[string][]extension.HandlerFn{
		"session_start": bridgeVoid(fake.OnSessionStartHandlers),
		"turn_end":      bridgeResult(fake.OnTurnEndHandlers),
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	for _, event := range []extension.ExtensionEvent{start, turn} {
		if _, err := runner.Emit(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	if gotStart != start || !reflect.DeepEqual(gotTurn, turn) {
		t.Fatalf("handlers saw %+v and %+v, want %+v and %+v", gotStart, gotTurn, start, turn)
	}
}

// TestConformanceAPIOnSessionBeforeEventsCancelLikeUpstream is the Go reference for pi.on("session_before_switch" | "session_before_fork" | "session_before_compact" | "session_before_tree") (extensions/types.ts:1566-1584): a handler registered through extension.API.On<Event> that returns cancel ends the dispatch and its result is the emit result, so a later handler never runs (runner.ts:1098-1101); without cancel every handler runs and the last result wins.
//
// mutation-checked: the mutant "Runner.Emit keeps running handlers after a cancel result" fails it.
func TestConformanceAPIOnSessionBeforeEventsCancelLikeUpstream(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		register func(api extension.API, cancel bool, ran *[]string)
		handlers func(f *extensiontest.Fake) []extension.HandlerFn
		event    extension.ExtensionEvent
		cancel   func(result any) bool
	}{
		{
			name: "session_before_switch",
			register: func(api extension.API, cancel bool, ran *[]string) {
				api.OnSessionBeforeSwitch(func(context.Context, extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error) {
					*ran = append(*ran, "first")
					return extension.SessionBeforeSwitchResult{Cancel: cancel}, nil
				})
				api.OnSessionBeforeSwitch(func(context.Context, extension.SessionBeforeSwitchEvent) (extension.SessionBeforeSwitchResult, error) {
					*ran = append(*ran, "second")
					return extension.SessionBeforeSwitchResult{}, nil
				})
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn {
				return bridgeResult(f.OnSessionBeforeSwitchHandlers)
			},
			event:  extension.SessionBeforeSwitchEvent{Type: "session_before_switch", Reason: "new"},
			cancel: func(r any) bool { v, ok := r.(extension.SessionBeforeSwitchResult); return ok && v.Cancel },
		},
		{
			name: "session_before_fork",
			register: func(api extension.API, cancel bool, ran *[]string) {
				api.OnSessionBeforeFork(func(context.Context, extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error) {
					*ran = append(*ran, "first")
					return extension.SessionBeforeForkResult{Cancel: cancel}, nil
				})
				api.OnSessionBeforeFork(func(context.Context, extension.SessionBeforeForkEvent) (extension.SessionBeforeForkResult, error) {
					*ran = append(*ran, "second")
					return extension.SessionBeforeForkResult{}, nil
				})
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeResult(f.OnSessionBeforeForkHandlers) },
			event:    extension.SessionBeforeForkEvent{Type: "session_before_fork", EntryID: "e1", Position: "at"},
			cancel:   func(r any) bool { v, ok := r.(extension.SessionBeforeForkResult); return ok && v.Cancel },
		},
		{
			name: "session_before_compact",
			register: func(api extension.API, cancel bool, ran *[]string) {
				api.OnSessionBeforeCompact(func(context.Context, extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error) {
					*ran = append(*ran, "first")
					return extension.SessionBeforeCompactResult{Cancel: cancel}, nil
				})
				api.OnSessionBeforeCompact(func(context.Context, extension.SessionBeforeCompactEvent) (extension.SessionBeforeCompactResult, error) {
					*ran = append(*ran, "second")
					return extension.SessionBeforeCompactResult{}, nil
				})
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn {
				return bridgeResult(f.OnSessionBeforeCompactHandlers)
			},
			event:  extension.SessionBeforeCompactEvent{Type: "session_before_compact"},
			cancel: func(r any) bool { v, ok := r.(extension.SessionBeforeCompactResult); return ok && v.Cancel },
		},
		{
			name: "session_before_tree",
			register: func(api extension.API, cancel bool, ran *[]string) {
				api.OnSessionBeforeTree(func(context.Context, extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error) {
					*ran = append(*ran, "first")
					return extension.SessionBeforeTreeResult{Cancel: cancel}, nil
				})
				api.OnSessionBeforeTree(func(context.Context, extension.SessionBeforeTreeEvent) (extension.SessionBeforeTreeResult, error) {
					*ran = append(*ran, "second")
					return extension.SessionBeforeTreeResult{}, nil
				})
			},
			handlers: func(f *extensiontest.Fake) []extension.HandlerFn { return bridgeResult(f.OnSessionBeforeTreeHandlers) },
			event:    extension.SessionBeforeTreeEvent{Type: "session_before_tree"},
			cancel:   func(r any) bool { v, ok := r.(extension.SessionBeforeTreeResult); return ok && v.Cancel },
		},
	}
	for _, tc := range cases {
		for _, cancel := range []bool{true, false} {
			name := tc.name + "/cancel"
			if !cancel {
				name = tc.name + "/pass"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				fake := &extensiontest.Fake{}
				var ran []string
				tc.register(fake, cancel, &ran)
				ext := extension.Extension{Path: "api-reference", Handlers: map[string][]extension.HandlerFn{tc.name: tc.handlers(fake)}}
				runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
				result, err := runner.Emit(t.Context(), tc.event)
				if err != nil {
					t.Fatal(err)
				}
				if got := tc.cancel(result); got != cancel {
					t.Fatalf("emit result %#v: cancel = %v, want %v", result, got, cancel)
				}
				want := []string{"first", "second"}
				if cancel {
					want = []string{"first"}
				}
				if !reflect.DeepEqual(ran, want) {
					t.Fatalf("handlers that ran = %v, want %v", ran, want)
				}
			})
		}
	}
}
