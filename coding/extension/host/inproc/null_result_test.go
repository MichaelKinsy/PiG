package inproc_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// observeOnly registers handlers for event that return what a subprocess
// handler returning undefined (Node) or None (Python) sends: JSON null.
func observeOnly(path string, events ...string) extension.Extension {
	ext := newFakeExtension(path)
	for _, event := range events {
		ext.Handlers[event] = []extension.HandlerFn{func(...any) (any, error) { return json.RawMessage("null"), nil }}
	}
	return ext
}

// Upstream skips an undefined handler result (runner.ts emitToolCall
// `if (handlerResult)`, emitToolResult/emitContext/emitBeforeProviderRequest
// `!== undefined`). A JSON null or typed nil pointer is "no result" too.
func TestObserveOnlyHandlersReturnNoResult(t *testing.T) {
	events := []string{"tool_call", "tool_result", "context", "before_provider_request", "user_bash", "input"}
	runner := inproc.NewRunner([]extension.Extension{observeOnly("/observe", events...)}, t.TempDir())
	ctx := context.Background()

	call, err := runner.EmitToolCall(ctx, extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: "c1"}, ToolName: "bash"})
	if err != nil || call != nil {
		t.Fatalf("EmitToolCall = %+v, %v; want nil, nil", call, err)
	}
	result, err := runner.EmitToolResult(ctx, extension.CustomToolResultEvent{ToolResultEventBase: extension.ToolResultEventBase{Type: "tool_result", ToolCallID: "c1", IsError: true}, ToolName: "bash"})
	if err != nil || result != nil {
		t.Fatalf("EmitToolResult = %+v, %v; want nil, nil", result, err)
	}
	messages := []extension.AgentMessage{map[string]any{"role": "user", "content": "hi"}}
	gotMessages, err := runner.EmitContext(ctx, messages)
	if err != nil || len(gotMessages) != 1 {
		t.Fatalf("EmitContext = %+v, %v; want the input unchanged", gotMessages, err)
	}
	payload := map[string]any{"model": "m"}
	gotPayload, err := runner.EmitBeforeProviderRequest(ctx, payload)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := gotPayload.(map[string]any); !ok || m["model"] != "m" {
		t.Fatalf("EmitBeforeProviderRequest = %#v; want the original payload", gotPayload)
	}
	bash, err := runner.EmitUserBash(ctx, extension.UserBashEvent{Type: "user_bash", Command: "ls"})
	if err != nil || bash != nil {
		t.Fatalf("EmitUserBash = %+v, %v; want nil, nil", bash, err)
	}
	var reported []extension.ExtensionError
	runner.AddErrorListener(func(e *extension.ExtensionError) { reported = append(reported, *e) })
	input, err := runner.EmitInput(ctx, "hello", nil, extension.InputSourceUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := input.(extension.InputEventResultContinue); !ok {
		t.Fatalf("EmitInput = %#v; want continue", input)
	}
	if len(reported) != 0 {
		t.Fatalf("observe-only input handler reported errors: %+v", reported)
	}
}

// A later observe-only handler must not discard an earlier handler's
// session_before_* result, and a null user_bash result must not win first-wins.
func TestObserveOnlyHandlerKeepsEarlierResults(t *testing.T) {
	first := newFakeExtension("/first")
	first.Handlers["session_before_compact"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"compaction":{"summary":"custom"}}`), nil
	}}
	later := observeOnly("/later", "session_before_compact", "user_bash")
	bashHandler := newFakeExtension("/bash")
	bashHandler.Handlers["user_bash"] = []extension.HandlerFn{func(...any) (any, error) {
		return &extension.UserBashEventResult{Result: map[string]any{"output": "remote", "cancelled": false, "truncated": false}}, nil
	}}
	runner := inproc.NewRunner([]extension.Extension{first, later, bashHandler}, t.TempDir())

	got, err := runner.Emit(context.Background(), extension.SessionBeforeCompactEvent{Type: "session_before_compact"})
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := got.(json.RawMessage)
	if !ok || string(raw) != `{"compaction":{"summary":"custom"}}` {
		t.Fatalf("session_before_compact result = %#v; want the first handler's result", got)
	}
	bash, err := runner.EmitUserBash(context.Background(), extension.UserBashEvent{Type: "user_bash", Command: "ls"})
	if err != nil || bash == nil || bash.Result.(map[string]any)["output"] != "remote" {
		t.Fatalf("EmitUserBash = %+v, %v; want the later handler's result", bash, err)
	}
}

// An in-process handler that returns a typed nil pointer returns no result.
func TestTypedNilToolCallResultIsNoResult(t *testing.T) {
	ext := newFakeExtension("/typed-nil")
	ext.Handlers["tool_call"] = []extension.HandlerFn{func(...any) (any, error) {
		return (*extension.ToolCallEventResult)(nil), nil
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	got, err := runner.EmitToolCall(context.Background(), extension.CustomToolCallEvent{ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call"}, ToolName: "bash"})
	if err != nil || got != nil {
		t.Fatalf("EmitToolCall = %+v, %v; want nil, nil", got, err)
	}
}

// A subprocess before_provider_headers handler sends the mutated headers back;
// they replace the current headers for the next handler and the request.
func TestBeforeProviderHeadersSubprocessResultReplacesHeaders(t *testing.T) {
	ext := newFakeExtension("/remote")
	ext.Handlers["before_provider_headers"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"a":"1","x-remote":"yes","drop":null}`), nil
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	in := extension.ProviderHeaders{"a": new("1"), "drop": new("x")}
	got, err := runner.EmitBeforeProviderHeaders(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got["x-remote"] == nil || *got["x-remote"] != "yes" || got["drop"] != nil || len(got) != 3 {
		t.Fatalf("headers = %v", got)
	}
}

// Removing the leading system message in context_with_system is reported and
// the handler's output is still used.
func TestContextWithSystemReportsDroppedSystemMessage(t *testing.T) {
	ext := newFakeExtension("/drop")
	ext.Handlers["context_with_system"] = []extension.HandlerFn{func(...any) (any, error) {
		return json.RawMessage(`{"messages":[{"role":"user","content":"only"}]}`), nil
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	var reported []string
	runner.AddErrorListener(func(e *extension.ExtensionError) { reported = append(reported, e.Error) })
	got, err := runner.EmitContextWithSystem(context.Background(), []extension.AgentMessage{map[string]any{"role": "system", "content": "p"}, map[string]any{"role": "user", "content": "u"}})
	if err != nil || len(got) != 1 {
		t.Fatalf("EmitContextWithSystem = %v, %v; want the handler's single message", got, err)
	}
	if len(reported) != 1 || !strings.Contains(reported[0], "removed the leading system message") {
		t.Fatalf("reported = %v", reported)
	}
}
