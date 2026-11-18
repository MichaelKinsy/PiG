package codingagent_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Binding the extensions of a start runs the session_start handlers, and the resources_discover handlers when one is registered (agent-session.ts:3433-3442). Pi's event loop
// takes a turn while a handler that awaits pends, and none while the handlers all return without awaiting: the bind may pend exactly when a handler that can pend is
// registered for one of the two events. A handler registered as one that returns without awaiting is not that, whatever extension holds it.
func TestBindMayPendAsksWhetherAHandlerCanPend(t *testing.T) {
	returns := func(...any) (any, error) { return nil, nil }
	build := func(register func(e *extension.Extension)) *inproc.Runner {
		e := extension.Extension{Path: "/bind-may-pend"}
		register(&e)
		return inproc.NewRunner([]extension.Extension{e}, t.TempDir())
	}
	for _, tc := range []struct {
		name     string
		register func(e *extension.Extension)
		want     bool
	}{
		{"no handler", func(*extension.Extension) {}, false},
		{"a session_start handler that returns without awaiting", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait(icodingagent.EventSessionStart, 1, returns)
		}, false},
		{"a session_start handler that can pend", func(e *extension.Extension) { e.AddEventHandler(icodingagent.EventSessionStart, 1, returns) }, true},
		{"a mixed set of session_start handlers", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait(icodingagent.EventSessionStart, 1, returns)
			e.AddEventHandler(icodingagent.EventSessionStart, 2, returns)
		}, true},
		{"a resources_discover handler that returns without awaiting", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait(icodingagent.EventResourcesDiscover, 1, returns)
		}, false},
		{"a resources_discover handler that can pend", func(e *extension.Extension) { e.AddEventHandler(icodingagent.EventResourcesDiscover, 1, returns) }, true},
		{"a handler that can pend for an event the bind does not run", func(e *extension.Extension) {
			e.AddEventHandlerReturningWithoutAwait(icodingagent.EventSessionStart, 1, returns)
			e.AddEventHandler("tool_call", 2, returns)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := icodingagent.BindMayPendForTest(build(tc.register)); got != tc.want {
				t.Errorf("bindMayPend = %v, want %v", got, tc.want)
			}
		})
	}
	if icodingagent.BindMayPendForTest(nil) {
		t.Error("bindMayPend(nil) = true")
	}
}
