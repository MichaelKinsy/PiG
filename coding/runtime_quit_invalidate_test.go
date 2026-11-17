package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's dispose() runs session.abort(), session_shutdown, beforeSessionInvalidate, session.dispose() in that order
// (agent-session-runtime.ts:415-424), at the place the quit's dispose is called. pig's EmitQuitShutdown is where its mode calls that dispose, so the hook runs
// there, once, and Close does not run it again.
func TestQuitDisposeRunsBeforeSessionInvalidateAfterSessionShutdown(t *testing.T) {
	for _, tc := range []struct {
		name string
		emit func(*Runtime)
	}{
		{"EmitQuitShutdown", func(rt *Runtime) { rt.EmitQuitShutdown() }},
		{"EmitQuitShutdownAfterInputEnd", func(rt *Runtime) { rt.EmitQuitShutdownAfterInputEnd(func() {}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var phases []string
			h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
				return extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_shutdown": {func(...any) (any, error) {
					phases = append(phases, "session_shutdown")
					return nil, nil
				}}}}
			}})
			h.runtime.SetBeforeSessionInvalidate(func() { phases = append(phases, "beforeSessionInvalidate") })
			tc.emit(h.runtime)
			if want := []string{"session_shutdown", "beforeSessionInvalidate"}; !reflect.DeepEqual(phases, want) {
				t.Fatalf("phases after the quit's dispose = %v, want %v", phases, want)
			}
			if err := h.runtime.Close(); err != nil {
				t.Fatal(err)
			}
			if want := []string{"session_shutdown", "beforeSessionInvalidate"}; !reflect.DeepEqual(phases, want) {
				t.Fatalf("phases after Close = %v, want the hook once: %v", phases, want)
			}
		})
	}
}

// A mode that emits through EmitQuitShutdownContext (print, RPC) leaves the hook to Close, which runs it once.
func TestQuitContextEmitLeavesTheInvalidateHookToClose(t *testing.T) {
	var phases []string
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	h.runtime.SetBeforeSessionInvalidate(func() { phases = append(phases, "beforeSessionInvalidate") })
	h.runtime.EmitQuitShutdownContext(t.Context())
	if len(phases) != 0 {
		t.Fatalf("EmitQuitShutdownContext ran the hook: %v", phases)
	}
	if err := h.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"beforeSessionInvalidate"}; !reflect.DeepEqual(phases, want) {
		t.Fatalf("phases after Close = %v, want %v", phases, want)
	}
}
