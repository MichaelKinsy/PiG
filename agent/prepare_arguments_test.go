package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// preparerTool is an AgentTool with Pi's prepareArguments hook (packages/agent/src/types.ts:473).
type preparerTool struct {
	scriptTool
	prepare func(json.RawMessage) (json.RawMessage, error)
	calls   int
}

func (t *preparerTool) PrepareArguments(raw json.RawMessage) (json.RawMessage, error) {
	t.calls++
	return t.prepare(raw)
}

func newPreparerTool(prepare func(json.RawMessage) (json.RawMessage, error)) (*preparerTool, *[]string) {
	executed := &[]string{}
	tool := &preparerTool{prepare: prepare}
	tool.scriptTool = scriptTool{name: "edit", params: map[string]any{
		"type":       "object",
		"properties": map[string]any{"edits": map[string]any{"type": "array"}},
		"required":   []any{"edits"},
	}, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		*executed = append(*executed, string(args))
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "edited"}}}, nil
	}}
	return tool, executed
}

func runPrepared(tool AgentTool, hooks ToolCallHooks, args ai.JsonObject) preparedOutcome {
	return runPreparedCall(RunToolCallOptions{Tools: []AgentTool{tool}, ToolCallHooks: hooks}, args)
}

// preparedOutcome is the part of RunToolCall's outcome these tests read.
type preparedOutcome struct {
	Text    string
	IsError bool
}

func runPreparedCall(options RunToolCallOptions, args ai.JsonObject) preparedOutcome {
	outcome, _ := RunToolCall(context.Background(), AgentToolCall{ID: "tool-1", Name: "edit", Arguments: args}, options)
	return preparedOutcome{Text: outcome.Result.Text(), IsError: outcome.IsError}
}

// .upstream/current/packages/agent/src/agent-loop.ts:725-726 and 770-776 (prepareToolCall): prepareArguments runs inside the try, so
// an error it throws becomes an error tool result carrying the message and the tool never executes.
func TestPrepareArguments_ErrorBecomesErrorResultAndToolDoesNotRun(t *testing.T) {
	tool, executed := newPreparerTool(func(json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("cannot normalize")
	})
	got := runPrepared(tool, ToolCallHooks{}, ai.JsonObject{"oldText": "a"})
	if !got.IsError || got.Text != "cannot normalize" {
		t.Fatalf("outcome = %+v, want error result \"cannot normalize\"", got)
	}
	if len(*executed) != 0 {
		t.Fatalf("tool executed %v after prepareArguments failed", *executed)
	}
}

// Same site: a throw (Go panic) inside prepareArguments is caught the same way.
func TestPrepareArguments_PanicBecomesErrorResultAndToolDoesNotRun(t *testing.T) {
	tool, executed := newPreparerTool(func(json.RawMessage) (json.RawMessage, error) { panic("boom") })
	got := runPrepared(tool, ToolCallHooks{}, ai.JsonObject{"oldText": "a"})
	if !got.IsError || got.Text != "boom" {
		t.Fatalf("outcome = %+v, want error result \"boom\"", got)
	}
	if len(*executed) != 0 {
		t.Fatalf("tool executed %v after prepareArguments panicked", *executed)
	}
}

// agent-loop.ts:726-727: the prepared arguments are validated against the tool schema ("prepare arguments for validation"), so
// output that still violates the schema fails validation and the tool does not run.
func TestPrepareArguments_PreparedArgumentsAreStillValidated(t *testing.T) {
	tool, executed := newPreparerTool(func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil })
	got := runPrepared(tool, ToolCallHooks{}, ai.JsonObject{"oldText": "a"})
	if !got.IsError || got.Text == "" {
		t.Fatalf("outcome = %+v, want a validation error", got)
	}
	if len(*executed) != 0 {
		t.Fatalf("tool executed %v with arguments that fail the schema", *executed)
	}
}

// agent-loop.ts:726-737 and 764-769: the arguments beforeToolCall sees and the tool executes with are the prepared, validated ones;
// the hook runs after preparation, and preparation happens once per call.
func TestPrepareArguments_HooksAndToolSeePreparedArguments(t *testing.T) {
	tool, executed := newPreparerTool(func(raw json.RawMessage) (json.RawMessage, error) {
		var in map[string]any
		_ = json.Unmarshal(raw, &in)
		return json.Marshal(map[string]any{"edits": []any{map[string]any{"oldText": in["oldText"]}}})
	})
	var hookSaw []string
	hooks := ToolCallHooks{BeforeToolCallHooks: []BeforeToolCallHook{func(_ context.Context, _, _ string, args json.RawMessage) ToolCallHookResult {
		hookSaw = append(hookSaw, string(args))
		return ToolCallHookResult{}
	}}}
	got := runPrepared(tool, hooks, ai.JsonObject{"oldText": "before"})
	if got.IsError {
		t.Fatalf("outcome = %+v", got)
	}
	want := []string{`{"edits":[{"oldText":"before"}]}`}
	if !reflect.DeepEqual(hookSaw, want) || !reflect.DeepEqual(*executed, want) {
		t.Fatalf("hook saw %v, tool executed %v, want %v", hookSaw, *executed, want)
	}
	if tool.calls != 1 {
		t.Fatalf("prepareArguments ran %d times, want 1", tool.calls)
	}
}

// agent-loop.ts:695-697: a tool without prepareArguments keeps its arguments as they are.
func TestPrepareArguments_ToolWithoutHookKeepsArguments(t *testing.T) {
	var executed []string
	tool := &scriptTool{name: "edit", params: map[string]any{"type": "object", "properties": map[string]any{"edits": map[string]any{"type": "array"}}, "required": []any{"edits"}},
		execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			executed = append(executed, string(args))
			return AgentToolResult{}, nil
		}}
	if got := runPrepared(tool, ToolCallHooks{}, ai.JsonObject{"edits": []any{}}); got.IsError {
		t.Fatalf("outcome = %+v", got)
	}
	if want := []string{`{"edits":[]}`}; !reflect.DeepEqual(executed, want) {
		t.Fatalf("executed = %v, want %v", executed, want)
	}
}

// agent-tool-typebox.test.ts is type-level (expectTypeOf): in Pi, execute() and prepareArguments() are typed by the parameter
// schema. Go's AgentTool takes JSON arguments, so the schema is enforced at run time: the arguments a tool executes with are the
// validated, prepared JSON of that schema (a required string command and an optional "fast" | "deep" mode).
func TestAgentToolArgumentsOfASchemaReachExecuteAsTheSchemaShape(t *testing.T) {
	type params struct {
		Command string  `json:"command"`
		Mode    *string `json:"mode"`
	}
	var executed []params
	tool := &preparerTool{prepare: func(raw json.RawMessage) (json.RawMessage, error) { return raw, nil }}
	tool.scriptTool = scriptTool{name: "bash", params: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string"},
			"mode":    map[string]any{"anyOf": []any{map[string]any{"const": "fast"}, map[string]any{"const": "deep"}}},
		},
		"required": []any{"command"},
	}, execute: func(_ context.Context, _ string, raw json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		var got params
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("arguments %s do not decode into the schema's shape: %v", raw, err)
		}
		executed = append(executed, got)
		return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ran"}}}, nil
	}}
	run := func(name string, args ai.JsonObject) preparedOutcome {
		outcome, _ := RunToolCall(context.Background(), AgentToolCall{ID: name, Name: "bash", Arguments: args}, RunToolCallOptions{Tools: []AgentTool{tool}})
		return preparedOutcome{Text: outcome.Result.Text(), IsError: outcome.IsError}
	}
	deep := "deep"
	if got := run("full", ai.JsonObject{"command": "ls", "mode": "deep"}); got.IsError {
		t.Fatalf("a call that matches the schema = %+v", got)
	}
	if got := run("bare", ai.JsonObject{"command": "pwd"}); got.IsError {
		t.Fatalf("a call without the optional mode = %+v", got)
	}
	for name, args := range map[string]ai.JsonObject{
		"badMode":    {"command": "ls", "mode": "slow"},
		"noCommand":  {"mode": "fast"},
		"badCommand": {"command": []any{}},
	} {
		if got := run(name, args); !got.IsError {
			t.Fatalf("%s: arguments the schema refuses ran: %+v", name, got)
		}
	}
	want := []params{{Command: "ls", Mode: &deep}, {Command: "pwd"}}
	if !reflect.DeepEqual(executed, want) {
		t.Fatalf("tool executed with %+v, want %+v", executed, want)
	}
}
