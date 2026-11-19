package extension_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func noop(...any) (any, error) { return nil, nil }

// A handler registered as one that returns without awaiting never makes HasEventHandlersThatMayAwait answer true; every other registration does: a handler added with
// AddEventHandler, a JSON handler of a subprocess extension and a legacy Handlers entry. A mixed set can pend. The answer is per event.
func TestHasEventHandlersThatMayAwaitAsksTheRegistrationOfEachHandler(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(e *extension.Extension)
		want  bool
	}{
		{"no handler", func(*extension.Extension) {}, false},
		{"only handlers that return without awaiting", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait("session_start", 1, noop)
			e.AddEventHandlerReturningWithoutAwait("session_start", 2, noop)
		}, false},
		{"a handler that can pend", func(e *extension.Extension) { e.AddEventHandler("session_start", 1, noop) }, true},
		{"a JSON handler", func(e *extension.Extension) { e.AddJSONEventHandler("session_start", 1, noop) }, true},
		{"a legacy Handlers entry", func(e *extension.Extension) {
			e.Handlers = map[string][]extension.HandlerFn{"session_start": {noop}}
		}, true},
		{"a mixed set", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait("session_start", 1, noop)
			e.AddEventHandler("session_start", 2, noop)
		}, true},
		{"the handler that can pend is of another event", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait("session_start", 1, noop)
			e.AddEventHandler("tool_call", 2, noop)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e extension.Extension
			tc.build(&e)
			if got := e.HasEventHandlersThatMayAwait("session_start"); got != tc.want {
				t.Errorf("HasEventHandlersThatMayAwait(session_start) = %v, want %v", got, tc.want)
			}
		})
	}
}

// Removing the handler that can pend leaves a set that cannot, and a copy of the registry made by ReplaceEventHandlers keeps each handler's registration.
func TestHandlerRegistrationSurvivesRemovalAndReplacement(t *testing.T) {
	var e extension.Extension
	e.AddEventHandlerReturningWithoutAwait("session_start", 1, noop)
	e.AddEventHandler("session_start", 2, noop)
	var copied extension.Extension
	copied.ReplaceEventHandlers(&e)
	if !copied.HasEventHandlersThatMayAwait("session_start") {
		t.Fatal("a copy of a registry with a handler that can pend answers false")
	}
	e.RemoveEventHandler("session_start", 2)
	if e.HasEventHandlersThatMayAwait("session_start") {
		t.Fatal("after the handler that can pend was removed, the registry still answers true")
	}
	if !e.HasEventHandlers("session_start") {
		t.Fatal("removing the handler that can pend removed the one that returns without awaiting")
	}
	copied.RemoveEventHandler("session_start", 1)
	if !copied.HasEventHandlersThatMayAwait("session_start") {
		t.Fatal("removing the handler that returns without awaiting dropped the registration of the one that can pend")
	}
}
