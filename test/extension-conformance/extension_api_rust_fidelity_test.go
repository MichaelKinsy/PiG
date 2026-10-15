package extensionconformance

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The Rust SDK twins of node_extension_api_fidelity_test.go (items D-G of the port-99-f6f-node review), in isolated and packed mode. B and C concern the factory-time Pi API, which only the Node runtime has.

// pi.unregisterVirtualModel (packages/coding-agent/src/core/extensions/types.ts:1875, loader.ts:511-514) reaches loader.ts:229-233: before the runner binds, unregisterVirtualModel filters the runtime-wide pending list, so a virtual model another extension queued is removed too.
func TestRustSDKUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingList(t *testing.T) {
	t.Parallel()
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		var pending []string
		for _, p := range h.host.Runtime().PendingVirtualModelRegistrations() {
			pending = append(pending, p.Definition.Provider+"/"+p.Definition.ID)
		}
		if want := []string{"conformance/auto", "conformance/identity"}; !slices.Equal(pending, want) {
			t.Fatalf("unregisterVirtualModel: pending virtual models %v, want %v", pending, want)
		}
	})
}

// agent-session.ts:788: a router that returns the state it was given keeps it, whatever bytes the SDK's encoder produced (serde_json orders object keys by name).
func TestRustSDKVirtualModelRouterReturningItsRequestStateKeepsTheRequestStateBytes(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		var identity extension.VirtualModelDefinition
		for _, p := range h.host.Runtime().PendingVirtualModelRegistrations() {
			if p.Definition.ID == "identity" {
				identity = p.Definition
			}
		}
		if identity.Route == nil {
			t.Fatal("no identity route")
		}
		for _, state := range []string{`{"a":"\u003cb\u003e\u0026","n":1}`, `{"z":1,"a":[1,2,{"k":null}]}`, `"\u003c"`, `7`} {
			route, err := identity.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "identity"}, Reason: extension.ModelRouteReasonContinuation, State: json.RawMessage(state)})
			if err != nil && !bytes.Contains([]byte(err.Error()), []byte("conformance/phys")) {
				t.Fatal(err)
			}
			if !bytes.Equal(route.State, json.RawMessage(state)) {
				t.Fatalf("a router that returned its request state %s produced %s, which the session would store as a new state (route error %v)", state, route.State, err)
			}
		}
	})
}

// runner.ts:958-961: the ctx.tools getter reads the callable tools when it is read, so a read after setActiveTools inside the same handler sees the change.
func TestRustSDKCtxToolsIsLiveInsideOneHandler(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		result := h.execute(t, "live_tools", "call-l")
		if want := rustAPICallableTool + ",only|only|only"; result.Text() != want {
			t.Fatalf("ctx.tools read %q, want %q", result.Text(), want)
		}
	})
}

// nested-tool-calls.ts:219-248 and agent-loop.ts:820-849: a panic in the caller's on_update is the callback's throw. It rejects the nested call after the tool returned, every partial result still reaches on_update, and the call never reaches afterToolCall or tool_execution_end.
func TestRustSDKExecuteToolOnUpdatePanicRejectsTheNestedCall(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		result := h.execute(t, "nested_throw", "call-t")
		if want := "rejected: callback boom after partial-one,partial-two"; result.Text() != want {
			t.Fatalf("the extension saw %q, want %q", result.Text(), want)
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if !slices.Equal(h.updateRejections, []string{"callback boom"}) {
			t.Fatalf("the host rejected the nested call with %v, want the callback's error once", h.updateRejections)
		}
	})
}

// A Rust handler that starts a host call on a thread and returns leaves the call running, as Pi's un-awaited call does (loader.ts createExtensionRuntime has no request scope; only the call's own AbortSignal ends it). TestExtensionAPIHostCallStartedByAHandlerOutlivesItsResponseGo is the Go SDK's row.
func TestRustSDKHostCallStartedByAHandlerOutlivesItsResponse(t *testing.T) {
	forEachRustAPIMode(t, func(t *testing.T, h *rustAPIHost) {
		dir := t.TempDir()
		args, err := json.Marshal(map[string]string{"dir": dir})
		if err != nil {
			t.Fatal(err)
		}
		type done struct {
			text string
			err  error
		}
		returned := make(chan done, 1)
		go func() {
			result, err := h.ext.Tools["detached_call"].Definition.Execute(t.Context(), "call-d", args, nil)
			text := ""
			if typed, ok := result, true; ok {
				text = typed.Text()
			}
			returned <- done{text, err}
		}()
		select {
		case <-h.waitStarted:
		case <-time.After(20 * time.Second):
			t.Fatal("the thread's nested call never reached the host")
		}
		if err := os.WriteFile(filepath.Join(dir, "started"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-returned:
			if got.err != nil || got.text != "returned" {
				t.Fatalf("detached_call = %q, %v", got.text, got.err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the handler never returned")
		}
		close(h.release)
		select {
		case got := <-h.waitEnded:
			if got != "released" {
				t.Fatalf("the nested call ended %q, want released", got)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("the nested call never ended")
		}
		var record []byte
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if data, err := os.ReadFile(filepath.Join(dir, "out")); err == nil && len(data) > 0 {
				record = data
				break
			}
		}
		if string(record) != "ok:released" {
			t.Fatalf("the thread's call recorded %q, want ok:released", record)
		}
	})
}
