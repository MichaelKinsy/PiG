package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The Python twins of node_extension_api_fidelity_test.go (items D-G of the port-99-f6f-node review). B and C concern the factory-time Pi API, which only the Node runtime has: a Python extension registers on its Extension before the connection exists, so it has no factory-time ownership check or registry read.

const pyVirtualModelOwnerFixture = `import pig_sdk

def route(ctx, request):
    return {"model": {"provider": "anthropic", "id": "x"}, "thinkingLevel": "off"}

def new_extension():
    e = pig_sdk.Extension("pyapi-vm-owner")
    e.register_virtual_model(pig_sdk.VirtualModel(provider="pyrouter", id="victim", name="Victim", route=route))
    e.register_virtual_model(pig_sdk.VirtualModel(provider="pyrouter", id="kept", name="Kept", route=route))
    return e
`

const pyVirtualModelRemoverFixture = `import pig_sdk

def new_extension():
    e = pig_sdk.Extension("pyapi-vm-remover")
    e.unregister_virtual_model("pyrouter", "victim")
    return e
`

// loader.ts:228-232: before the runner binds, unregisterVirtualModel filters the runtime-wide pending list, so a virtual model an earlier-loaded extension queued is removed too.
func TestPythonSDKUnregisterVirtualModelBeforeBindFiltersTheRuntimeWidePendingList(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, nil, pyAPIFixture{"pyapi-vm-owner", pyVirtualModelOwnerFixture}, pyAPIFixture{"pyapi-vm-remover", pyVirtualModelRemoverFixture})
		var pending []string
		for _, p := range rig.host.Runtime().PendingVirtualModelRegistrations() {
			pending = append(pending, p.Definition.Provider+"/"+p.Definition.ID)
		}
		if want := []string{"pyrouter/kept"}; !slices.Equal(pending, want) {
			t.Fatalf("pending virtual models %v, want %v", pending, want)
		}
	})
}

const pyVirtualStateIdentityFixture = `import pig_sdk

def route(ctx, request):
    return {"model": {"provider": "anthropic", "id": "py-picked-user"}, "thinkingLevel": "off", "state": request.get("state")}

def new_extension():
    e = pig_sdk.Extension("pyapi-identity")
    e.register_virtual_model(pig_sdk.VirtualModel(provider="pyrouter", id="identity", name="Identity", route=route))
    return e
`

// agent-session.ts:788: a router that returns the state it was given keeps it. The SDK encodes the returned value with Python's own JSON bytes, which differ from the bytes the Host sent (separators, escapes, key order kept), so the Host compares the JSON values.
func TestPythonSDKVirtualModelRouterReturningItsRequestStateKeepsTheRequestStateBytes(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		rig := newPyAPIRig(t, isolation, &subprocess.HostCallbacks{}, pyAPIFixture{"pyapi-identity", pyVirtualStateIdentityFixture})
		def := rig.host.Runtime().PendingVirtualModelRegistrations()[0].Definition
		for _, state := range []string{`{"a":"\u003cb\u003e\u0026","n":1}`, `{"z":1,"a":[1,2,{"k":null}]}`, `"\u003c"`, `7`} {
			route, err := def.Route(t.Context(), extension.ModelRouteRequest{Model: &ai.Model{ID: "identity"}, Reason: extension.ModelRouteReasonContinuation, State: json.RawMessage(state)})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(route.State, json.RawMessage(state)) {
				t.Fatalf("a router that returned its request state %s produced %s, which the session would store as a new state", state, route.State)
			}
		}
	})
}

const pyLiveToolsFixture = pyAPIPrelude + `
def new_extension():
    e = pig_sdk.Extension("pyapi-live")

    def live_tools(ctx, args):
        names = lambda: ",".join(t["name"] for t in ctx.tools)
        before = names()
        ctx.set_active_tools(["only"])
        after = names()
        return "%s|%s|%s" % (before, after, names())

    e.tool("live_tools", "Reads ctx.tools around set_active_tools", {"type": "object"}, live_tools)
    return e
`

// runner.ts:958-961: the ctx.tools getter reads the callable tools when it is read, so a read after setActiveTools inside the same handler sees the change.
func TestPythonSDKCtxToolsIsLiveInsideOneHandler(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		active := []string{"a", "b", "c"}
		actions := &subprocess.HostCallbacks{
			SetActiveTools: func(names []string) {
				mu.Lock()
				active = slices.Clone(names)
				mu.Unlock()
			},
			GetCallableTools: func() []extension.AgentTool {
				mu.Lock()
				defer mu.Unlock()
				var tools []extension.AgentTool
				for _, name := range active {
					tools = append(tools, extension.AgentTool{Name: name, Label: name, Parameters: json.RawMessage(`{"type":"object"}`)})
				}
				return tools
			},
		}
		rig := newPyAPIRig(t, isolation, actions, pyAPIFixture{"pyapi-live", pyLiveToolsFixture})
		result, err := rig.execute("pyapi-live", "live_tools", "call-l")
		if err != nil || result.IsError {
			t.Fatalf("live_tools = %+v, %v", result, err)
		}
		if want := "a,b,c|only|only"; result.Text() != want {
			t.Fatalf("ctx.tools read %q, want %q", result.Text(), want)
		}
	})
}

const pyNestedThrowFixture = pyAPIPrelude + `
def new_extension():
    e = pig_sdk.Extension("pyapi-throw")

    def nest_throw(ctx, args):
        seen = []

        def on_update(partial):
            seen.append(partial["content"][0]["text"])
            if len(seen) == 1:
                raise ValueError("callback boom")

        try:
            ctx.execute_tool("slow", {}, pig_sdk.ExecuteToolOptions(on_update=on_update))
            return "no rejection after " + ",".join(seen)
        except Exception as exc:
            return "rejected: %s after %s" % (exc, ",".join(seen))

    e.tool("nest_throw", "Raises from on_update", {"type": "object"}, nest_throw)
    return e
`

// nested-tool-calls.ts:219-248 and agent-loop.ts:820-849: an exception from the caller's on_update rejects the nested call after the tool returned, every partial result still reaches on_update, and the call never reaches afterToolCall or tool_execution_end.
func TestPythonSDKExecuteToolOnUpdateRaiseRejectsTheNestedCall(t *testing.T) {
	eachPyAPIIsolation(t, func(t *testing.T, isolation string) {
		var mu sync.Mutex
		var sinkResults []string
		var completed []string
		actions := &subprocess.HostCallbacks{
			ExecuteTool: func(_ context.Context, callerID, name string, _ json.RawMessage, options extension.ExecuteToolOptions) (extension.AgentToolCallOutcome, error) {
				sink, _ := options.OnUpdate.(func(agent.AgentToolResult) error)
				if sink == nil {
					return extension.AgentToolCallOutcome{}, nil
				}
				var first error
				for _, step := range []string{"one", "two"} {
					err := sink(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: step}}, Details: map[string]any{"step": step}})
					mu.Lock()
					if err != nil {
						sinkResults = append(sinkResults, step+": "+err.Error())
					} else {
						sinkResults = append(sinkResults, step+": ok")
					}
					mu.Unlock()
					if err != nil && first == nil {
						first = err
					}
				}
				if first != nil {
					return extension.AgentToolCallOutcome{}, first
				}
				mu.Lock()
				completed = append(completed, name)
				mu.Unlock()
				return extension.AgentToolCallOutcome{ToolCall: ai.ToolCall{ID: callerID + "/1", Name: name, Arguments: ai.JsonObject{}}, Result: agent.AgentToolResult{}}, nil
			},
		}
		rig := newPyAPIRig(t, isolation, actions, pyAPIFixture{"pyapi-throw", pyNestedThrowFixture})
		result, err := rig.execute("pyapi-throw", "nest_throw", "call-t")
		if err != nil || result.IsError {
			t.Fatalf("nest_throw = %+v, %v", result, err)
		}
		if want := "rejected: callback boom after one,two"; result.Text() != want {
			t.Fatalf("the extension saw %q, want %q", result.Text(), want)
		}
		mu.Lock()
		defer mu.Unlock()
		if want := []string{"one: callback boom", "two: ok"}; !slices.Equal(sinkResults, want) {
			t.Fatalf("the update sink returned %v, want %v", sinkResults, want)
		}
		if len(completed) != 0 {
			t.Fatalf("the call completed %v although its update callback raised", completed)
		}
	})
}
