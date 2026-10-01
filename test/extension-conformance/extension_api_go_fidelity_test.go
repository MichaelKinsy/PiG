package extensionconformance

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The Go SDK twins of node_extension_api_fidelity_test.go (items D-G of the port-99-f6f-node review), through the fused, isolated and packed placements of the one fixture factory. B and C concern the factory-time Pi API, which only the Node runtime has.

// loader.ts:228-232: before the runner binds, unregisterVirtualModel filters the runtime-wide pending list, so a virtual model another extension queued is removed too. The harness queues router/victim as an earlier-loaded extension would, and the fixture's factory unregisters it.
func TestExtensionAPIUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingListGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			h.mu.Lock()
			defer h.mu.Unlock()
			if _, bound := h.virtualModels["router/victim"]; bound {
				t.Fatal("a virtual model another extension queued survived the unregistration the fixture's factory made")
			}
			if _, bound := h.virtualModels["router/auto"]; !bound {
				t.Fatal("the fixture's own virtual model was not applied")
			}
		})
	}
}

// agent-session.ts:788: a router that returns the state it was given keeps it, whatever bytes the SDK's encoder produced.
func TestExtensionAPIVirtualModelRouterReturningItsRequestStateKeepsTheRequestStateBytesGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			h.mu.Lock()
			identity := h.virtualModels["router/identity"]
			h.mu.Unlock()
			if identity.Route == nil {
				t.Fatal("no route")
			}
			for _, state := range []string{`{"a":"\u003cb\u003e\u0026","n":1}`, `{"z":1,"a":[1,2,{"k":null}]}`, `"\u003c"`, `7`} {
				route, err := identity.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "identity"}, Reason: extension.ModelRouteReasonContinuation, State: json.RawMessage(state)})
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(route.State, json.RawMessage(state)) {
					t.Fatalf("a router that returned its request state %s produced %s, which the session would store as a new state", state, route.State)
				}
			}
		})
	}
}

// runner.ts:958-961: the ctx.tools getter reads the callable tools when it is read, so a read after setActiveTools inside the same handler sees the change.
func TestExtensionAPICtxToolsIsLiveInsideOneHandlerGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			result, err := h.run(t.Context(), "live_tools", "call-l", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			want := "echo,helper,soft_fail,nested_updates,nested_cancel,nested_own_signal,probe_state,live_tools,nested_throw,detached_call,register_late,unregister_late|echo"
			if result.Text() != want {
				t.Fatalf("ctx.tools read %q, want %q", result.Text(), want)
			}
		})
	}
}

// nested-tool-calls.ts:219-248 and agent-loop.ts:820-849: a panic in the caller's OnUpdate is the callback's throw. It rejects the nested call after the tool returned, every partial result still reaches OnUpdate, and the call never reaches afterToolCall or tool_execution_end.
func TestExtensionAPIExecuteToolOnUpdatePanicRejectsTheNestedCallGo(t *testing.T) {
	for _, placement := range extensionAPIPlacements() {
		t.Run(placement, func(t *testing.T) {
			h := newExtensionAPIHarness(t, placement)
			result, err := h.run(t.Context(), "nested_throw", "call-t", `{}`)
			if err != nil {
				t.Fatal(err)
			}
			if want := "rejected: callback boom after one,two"; result.Text() != want {
				t.Fatalf("the extension saw %q, want %q", result.Text(), want)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			if len(h.updateRejections) != 1 || h.updateRejections[0] != "callback boom" {
				t.Fatalf("the host rejected the nested call with %v, want the callback's error once", h.updateRejections)
			}
		})
	}
}
