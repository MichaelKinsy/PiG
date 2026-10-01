package sdk

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func nestedOutcome(id, name, text string, isError bool) string {
	flag := "false"
	if isError {
		flag = "true"
	}
	return `{"toolCall":{"type":"toolCall","id":"` + id + `","name":"` + name + `","arguments":{}},"result":{"content":[{"type":"text","text":"` + text + `"}],"details":{}},"isError":` + flag + `}`
}

// Upstream agent-session-tool-orchestration.test.ts:41-50 (the `run_tools` tool, verbatim) and types.ts:386-394: a tool runs other tools through executeTool, each nested call gets `<calling id>/<n>`, an unknown tool comes back as an error outcome, and the extension composes the text the way the upstream tool does.
func TestExecuteToolRunsNestedCallsForTheCallingTool(t *testing.T) {
	ext := New("orchestration")
	ext.RegisterTool(ToolDefinition{
		Name: "run_tools", Label: "run_tools", Description: "Runs tools.", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			helper, err := ctx.ExecuteTool("helper", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			echo, err := ctx.ExecuteTool("echo", map[string]any{"text": "hi"}, nil)
			if err != nil {
				return nil, err
			}
			self, err := ctx.ExecuteTool("run_tools", map[string]any{}, nil)
			if err != nil {
				return nil, err
			}
			if helper.ToolCall.ID != "call-7/1" || helper.IsError || !self.IsError || self.Result.IsError {
				t.Errorf("outcomes = %+v %+v %+v", helper, echo, self)
			}
			return strings.Join([]string{helper.Result.Text(), echo.Result.Text(), self.Result.Text()}, " | "), nil
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	replies := map[string]string{
		"helper":    nestedOutcome("call-7/1", "helper", "helped", false),
		"echo":      nestedOutcome("call-7/2", "echo", "echo: hi", false),
		"run_tools": nestedOutcome("call-7/3", "run_tools", "Tool run_tools not found", true),
	}
	calls, resp := runSurfaceTool(t, host, "run_tools", "call-7", func(call *callMsg) *callResultMsg {
		var args struct{ Name string }
		_ = json.Unmarshal(call.Args, &args)
		return &callResultMsg{Result: json.RawMessage(replies[args.Name])}
	})
	if resp.Error != nil {
		t.Fatalf("tool failed: %+v", resp.Error)
	}
	if string(resp.Result) != `"helped | echo: hi | Tool run_tools not found"` {
		t.Fatalf("tool result = %s", resp.Result)
	}
	if len(calls) != 3 {
		t.Fatalf("calls = %+v", calls)
	}
	ids := map[string]bool{}
	for i, name := range []string{"helper", "echo", "run_tools"} {
		var args struct {
			CallerID     string          `json:"callerId"`
			Name         string          `json:"name"`
			Args         json.RawMessage `json:"args"`
			ExecuteID    string          `json:"executeId"`
			WantsUpdates bool            `json:"wantsUpdates"`
		}
		if calls[i].Method != "executeTool" || json.Unmarshal(calls[i].Args, &args) != nil {
			t.Fatalf("call %d = %+v", i, calls[i])
		}
		wantArgs := `{}`
		if name == "echo" {
			wantArgs = `{"text":"hi"}`
		}
		if args.CallerID != "call-7" || args.Name != name || string(args.Args) != wantArgs || args.WantsUpdates || args.ExecuteID == "" || ids[args.ExecuteID] {
			t.Fatalf("call %d args = %s", i, calls[i].Args)
		}
		ids[args.ExecuteID] = true
	}
}

// Upstream types.ts:367-372 (onUpdate) and runner.ts:966-983: partial results reach OnUpdate in order and before executeTool returns. The callback runs off the extension's message loop, so a slow callback does not hold up the host's other messages.
func TestExecuteToolDeliversUpdatesInOrderOffTheMessageLoop(t *testing.T) {
	ext := New("orchestration")
	var mu sync.Mutex
	var updates []string
	entered := make(chan struct{})
	release := make(chan struct{})
	settingsSeen := make(chan Settings, 1)
	returned := make(chan AgentToolCallOutcome, 1)
	ext.Command("peek", "", func(ctx Context, _ string) error {
		settings, err := ctx.GetSettings()
		settingsSeen <- settings
		return err
	})
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			outcome, err := ctx.ExecuteTool("slow", nil, &ExecuteToolOptions{OnUpdate: func(partial AgentToolResult) {
				mu.Lock()
				first := len(updates) == 0
				updates = append(updates, partial.Text())
				mu.Unlock()
				if first {
					close(entered)
					<-release
				}
			}})
			returned <- outcome
			return "done", err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()

	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-1", Args: json.RawMessage(`{}`)}})
	var call *envelope
	for call == nil {
		if env := host.readEnvelope(t); env.Type == msgCall {
			call = &env
		}
	}
	var args struct {
		ExecuteID    string `json:"executeId"`
		WantsUpdates bool   `json:"wantsUpdates"`
	}
	if err := json.Unmarshal(call.Call.Args, &args); err != nil || !args.WantsUpdates {
		t.Fatalf("call args = %s (%v)", call.Call.Args, err)
	}
	// The host sends each update as a request and waits for the answer before the next one and before the call's result.
	sendUpdate := func(text string) string {
		update := `{"executeId":"` + args.ExecuteID + `","result":{"content":[{"type":"text","text":"` + text + `"}],"details":{"step":"` + text + `"}}}`
		host.writeEnvelope(t, envelope{Type: msgRequest, ID: "update-" + text, Request: &requestMsg{Method: "execute_tool_update", Args: json.RawMessage(update)}})
		return "update-" + text
	}
	awaitResponse := func(id string) *responseMsg {
		for {
			if env := host.readEnvelope(t); env.Type == msgResponse && env.ID == id {
				return env.Response
			}
		}
	}
	first := sendUpdate("one")
	recv(t, entered)

	// The first callback is blocked. The message loop still applies a state update and starts a command.
	sendState(t, host, `{"settings":{"seen":"while the callback blocks"}}`)
	_, resp := runSurfaceCommand(t, host, "peek", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if got := recv(t, settingsSeen); !reflect.DeepEqual(got, Settings{"seen": "while the callback blocks"}) {
		t.Fatalf("settings = %#v", got)
	}
	select {
	case <-returned:
		t.Fatal("executeTool returned before its updates were delivered")
	default:
	}
	close(release)
	if resp := awaitResponse(first); resp.Error != nil {
		t.Fatalf("update one answered %+v", resp.Error)
	}
	for _, text := range []string{"two", "three"} {
		if resp := awaitResponse(sendUpdate(text)); resp.Error != nil {
			t.Fatalf("update %s answered %+v", text, resp.Error)
		}
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: call.ID, CallResult: &callResultMsg{Result: json.RawMessage(nestedOutcome("call-1/1", "slow", "final", false))}})
	awaitResponse("req-outer")
	outcome := recv(t, returned)
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(updates, []string{"one", "two", "three"}) {
		t.Fatalf("updates = %v, want them in order", updates)
	}
	if outcome.ToolCall.ID != "call-1/1" || outcome.Result.Text() != "final" || outcome.IsError {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// Upstream types.ts:367-372 (signal) and runner.ts:981: a cancelled options.Signal cancels the nested call; the host answers with an error outcome, which executeTool returns rather than raising.
func TestExecuteToolSignalCancelsTheNestedCall(t *testing.T) {
	ext := New("orchestration")
	signal, cancel := context.WithCancel(context.Background())
	got := make(chan AgentToolCallOutcome, 1)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			outcome, err := ctx.ExecuteTool("slow", nil, &ExecuteToolOptions{Signal: signal})
			got <- outcome
			return "done", err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-2", Args: json.RawMessage(`{}`)}})
	var execute envelope
	for execute.Call == nil {
		if env := host.readEnvelope(t); env.Type == msgCall {
			execute = env
		}
	}
	var started struct {
		ExecuteID string `json:"executeId"`
	}
	if err := json.Unmarshal(execute.Call.Args, &started); err != nil || execute.Call.Method != "executeTool" {
		t.Fatalf("call = %+v", execute.Call)
	}
	cancel()
	var cancelCall envelope
	for cancelCall.Call == nil {
		if env := host.readEnvelope(t); env.Type == msgCall {
			cancelCall = env
		}
	}
	if cancelCall.Call.Method != "executeTool.cancel" || string(cancelCall.Call.Args) != `{"executeId":"`+started.ExecuteID+`"}` {
		t.Fatalf("cancel call = %s %s", cancelCall.Call.Method, cancelCall.Call.Args)
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: cancelCall.ID, CallResult: &callResultMsg{}})
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: execute.ID, CallResult: &callResultMsg{Result: json.RawMessage(nestedOutcome("call-2/1", "slow", "Aborted", true))}})
	outcome := recv(t, got)
	if !outcome.IsError || outcome.Result.Text() != "Aborted" {
		t.Fatalf("outcome = %+v", outcome)
	}
}

// Upstream runner.ts:980 (`signal: options.signal ?? signal`): an explicit options.Signal replaces the calling tool's signal, so cancelling the calling request neither cancels the nested call nor ends executeTool; the call waits for the nested tool's outcome. The call is therefore not tied to the calling request on the wire.
func TestExecuteToolExplicitSignalReplacesTheCallingRequest(t *testing.T) {
	ext := New("orchestration")
	signal, cancelSignal := context.WithCancel(context.Background())
	defer cancelSignal()
	type result struct {
		outcome AgentToolCallOutcome
		err     error
	}
	got := make(chan result, 1)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			outcome, err := ctx.ExecuteTool("slow", nil, &ExecuteToolOptions{Signal: signal})
			got <- result{outcome, err}
			return "done", err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-4", Args: json.RawMessage(`{}`)}})
	var execute envelope
	for execute.Call == nil {
		if env := host.readEnvelope(t); env.Type == msgCall {
			execute = env
		}
	}
	if execute.Call.Method != "executeTool" || execute.Call.ParentRequestID != "" {
		t.Fatalf("call = %s with parent %q, want executeTool without the calling request as parent", execute.Call.Method, execute.Call.ParentRequestID)
	}
	host.writeEnvelope(t, envelope{Type: msgCancel, ID: "req-outer"})
	select {
	case r := <-got:
		t.Fatalf("executeTool ended with the calling request: %+v, %v", r.outcome, r.err)
	case <-time.After(200 * time.Millisecond):
	}
	host.writeEnvelope(t, envelope{Type: msgCallResult, ID: execute.ID, CallResult: &callResultMsg{Result: json.RawMessage(nestedOutcome("call-4/1", "slow", "finished", false))}})
	r := recv(t, got)
	if r.err != nil || r.outcome.IsError || r.outcome.Result.Text() != "finished" {
		t.Fatalf("outcome = %+v, error = %v, want the nested tool's outcome", r.outcome, r.err)
	}
}

// Upstream runner.ts:966-983: the calling tool's own cancellation is the default cancel of a nested call, so a cancelled request returns from executeTool with an error instead of waiting for a host that abandoned it.
func TestExecuteToolEndsWithTheCallingRequest(t *testing.T) {
	ext := New("orchestration")
	started := make(chan struct{})
	result := make(chan error, 1)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			close(started)
			_, err := ctx.ExecuteTool("slow", nil, nil)
			result <- err
			return nil, err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	watchdog := time.AfterFunc(10*time.Second, host.close)
	defer watchdog.Stop()
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "req-outer", Request: &requestMsg{Method: "tool_call", Tool: "outer", ToolCallID: "call-3", Args: json.RawMessage(`{}`)}})
	recv(t, started)
	for {
		if env := host.readEnvelope(t); env.Type == msgCall {
			if env.Call.ParentRequestID != "req-outer" {
				t.Fatalf("executeTool parent = %q, want the calling request", env.Call.ParentRequestID)
			}
			break
		}
	}
	host.writeEnvelope(t, envelope{Type: msgCancel, ID: "req-outer"})
	if err := recv(t, result); err == nil {
		t.Fatal("executeTool returned no error for a cancelled request")
	}
}

// Upstream types.ts:385-394: `tools` and `executeTool` exist on a tool's context only. Outside a tool handler there is no calling id, so the call fails without a host call.
func TestToolOrchestrationIsOnlyAvailableInsideATool(t *testing.T) {
	ext := New("orchestration")
	type outcome struct{ toolsErr, execErr error }
	got := make(chan outcome, 1)
	ext.Command("outside", "", func(ctx Context, _ string) error {
		_, toolsErr := ctx.Tools()
		_, execErr := ctx.ExecuteTool("x", nil, nil)
		got <- outcome{toolsErr, execErr}
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "outside", func(*callMsg) *callResultMsg { return &callResultMsg{} })
	o := recv(t, got)
	if resp.Error != nil || len(calls) != 0 || o.toolsErr == nil || o.execErr == nil {
		t.Fatalf("calls = %+v, errors = %v, %v", calls, o.toolsErr, o.execErr)
	}
}

// Upstream types.ts:385 and runner.ts:958-961: `tools` are the callable tools the host lists when the getter is read, so a read after the host changed its list sees the change.
func TestToolsReadsTheHostsCallableToolsWhenItIsRead(t *testing.T) {
	ext := New("orchestration")
	got := make(chan []AgentTool, 2)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			tools, err := ctx.Tools()
			got <- tools
			return "ok", err
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	listing := `{"tools":[{"name":"echo","label":"echo","description":"Echo text.","parameters":{"type":"object"},"outputSchema":{"type":"object"},"executionMode":"parallel"},{"name":"helper","label":"helper","description":"h","parameters":{}}]}`
	run := func(id string) []AgentTool {
		t.Helper()
		calls, resp := runSurfaceTool(t, host, "outer", id, func(*callMsg) *callResultMsg { return &callResultMsg{Result: json.RawMessage(listing)} })
		if resp.Error != nil || len(calls) != 1 || calls[0].Method != "getCallableTools" {
			t.Fatalf("calls = %+v, response = %+v (tools must ask the host)", calls, resp)
		}
		return recv(t, got)
	}
	first := run("c1")
	if len(first) != 2 || first[0].Name != "echo" || first[0].Description != "Echo text." || string(first[0].OutputSchema) != `{"type":"object"}` || first[0].ExecutionMode != "parallel" || first[1].Name != "helper" {
		t.Fatalf("tools = %+v", first)
	}
	listing = `{"tools":[{"name":"only","label":"only","description":"o","parameters":{}}]}`
	if second := run("c2"); len(second) != 1 || second[0].Name != "only" {
		t.Fatalf("tools after the host changed its list = %+v", second)
	}
}

// A host or transport failure of the call is the caller's error; it is not turned into an outcome the model would read as a tool failure.
func TestExecuteToolReturnsHostFailureAndEncodingErrors(t *testing.T) {
	ext := New("orchestration")
	type outcome struct{ hostErr, encodeErr error }
	got := make(chan outcome, 1)
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			_, hostErr := ctx.ExecuteTool("x", nil, nil)
			_, encodeErr := ctx.ExecuteTool("x", make(chan int), nil)
			got <- outcome{hostErr, encodeErr}
			return "ok", nil
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, _ := runSurfaceTool(t, host, "outer", "c1", func(*callMsg) *callResultMsg {
		return &callResultMsg{Error: &errorInfo{Message: "boom"}}
	})
	o := recv(t, got)
	if len(calls) != 1 || o.hostErr == nil || !strings.Contains(o.hostErr.Error(), "boom") || o.encodeErr == nil {
		t.Fatalf("calls = %d, errors = %v, %v", len(calls), o.hostErr, o.encodeErr)
	}
}

// Upstream runner.ts:979-981 (`{ ...options, signal: options.signal ?? signal }`): a nested tool runs with the signal its caller passed, and with the calling tool's otherwise, so its `signal` parameter differs only in the first case. The call tells the host which case it is.
func TestExecuteToolMarksAnExplicitSignalOnTheWire(t *testing.T) {
	ext := New("orchestration")
	own := t.Context()
	ext.RegisterTool(ToolDefinition{
		Name: "outer", Label: "outer", Description: "outer", Parameters: Schema{"type": "object"},
		Execute: func(ctx Context, _ map[string]any) (any, error) {
			for _, options := range []*ExecuteToolOptions{nil, {}, {Signal: own}} {
				if _, err := ctx.ExecuteTool("inner", nil, options); err != nil {
					return nil, err
				}
			}
			return "done", nil
		},
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceTool(t, host, "outer", "call-9", func(*callMsg) *callResultMsg {
		return &callResultMsg{Result: json.RawMessage(nestedOutcome("call-9/1", "inner", "ok", false))}
	})
	if resp.Error != nil || len(calls) != 3 {
		t.Fatalf("calls = %d, response error = %+v", len(calls), resp.Error)
	}
	for i, want := range []bool{false, false, true} {
		var args struct {
			OwnSignal bool `json:"ownSignal"`
		}
		if err := json.Unmarshal(calls[i].Args, &args); err != nil || args.OwnSignal != want {
			t.Errorf("call %d args = %s, want ownSignal=%t", i, calls[i].Args, want)
		}
	}
}
