package execution_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/execution"
	"github.com/MichaelKinsy/PiG/ai"
)

func toolCall(arguments ai.JsonObject) *ai.ToolCall {
	if arguments == nil {
		arguments = ai.JsonObject{"value": "input"}
	}
	return &ai.ToolCall{ID: "call-1", Name: "echo", Arguments: arguments}
}

func echoTool() harness.AgentHarnessTool {
	return harness.AgentHarnessTool{
		ToolSchema: ai.ToolSchema{Name: "echo", Description: "Echo input", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []any{"value"},
		}}, Label: "Echo",
		Execute: func(_ harness.Context, _ string, args map[string]any, _ harness.AgentHarnessToolUpdateCallback, _ any, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: args["value"].(string)}}, Details: map[string]any{"value": args["value"]}}, nil
		},
	}
}

func resultText(result harness.AgentToolResult) string {
	var text []string
	for _, block := range result.Content {
		if block, ok := block.(ai.TextContent); ok {
			text = append(text, block.Text)
		}
	}
	return strings.Join(text, "\n")
}

func clearedTool(t *testing.T, tool harness.AgentHarnessTool) *execution.ClearedToolCall {
	t.Helper()
	prepared, immediate := execution.PrepareToolCall(toolCall(nil), []harness.AgentHarnessTool{tool})
	if immediate != nil || prepared == nil {
		t.Fatalf("expected prepared call, got %#v / %#v", prepared, immediate)
	}
	cleared, immediate := execution.ApplyBeforeToolDecision(prepared, nil)
	if immediate != nil || cleared == nil {
		t.Fatalf("expected cleared call, got %#v / %#v", cleared, immediate)
	}
	return cleared
}

// The upstream invocation fixture supplies unused memo callbacks and fixed invocation identities.
type toolInvocation struct{}

func (toolInvocation) InvocationID() string { return "result-1" }
func (toolInvocation) OperationID() string  { return "operation-1" }
func (toolInvocation) TurnID() string       { return "turn-1" }
func (toolInvocation) GetMemo(harness.Context, string) (harness.JsonValue, bool, error) {
	return nil, false, nil
}
func (toolInvocation) SetMemo(harness.Context, string, *harness.JsonValue) error { return nil }

func TestPortWave01ExecutionTools(t *testing.T) {
	t.Parallel()
	// upstream: packages/agent/test/harness/execution-tools.test.ts:70
	t.Run("prepares arguments before validation and preserves the provider call", func(t *testing.T) {
		providerCall := toolCall(ai.JsonObject{"legacy": "prepared"})
		tool := echoTool()
		tool.PrepareArguments = func(args any) (any, error) { return map[string]any{"value": args.(ai.JsonObject)["legacy"]}, nil }
		prepared, immediate := execution.PrepareToolCall(providerCall, []harness.AgentHarnessTool{tool})
		if immediate != nil || prepared == nil {
			t.Fatalf("expected prepared call, got %#v / %#v", prepared, immediate)
		}
		if prepared.ToolCall != providerCall {
			t.Fatal("provider call identity changed")
		}
		requireEqual(t, prepared.Args, map[string]any{"value": "prepared"})
		requireEqual(t, providerCall.Arguments, ai.JsonObject{"legacy": "prepared"})
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:87
	t.Run("returns immediate errors for unknown tools, preparation throws, and invalid arguments", func(t *testing.T) {
		_, unknown := execution.PrepareToolCall(toolCall(nil), []harness.AgentHarnessTool{})
		tool := echoTool()
		tool.PrepareArguments = func(any) (any, error) { panic(errors.New("cannot prepare")) }
		_, preparationFailure := execution.PrepareToolCall(toolCall(nil), []harness.AgentHarnessTool{tool})
		_, invalid := execution.PrepareToolCall(toolCall(ai.JsonObject{}), []harness.AgentHarnessTool{echoTool()})
		if unknown == nil || preparationFailure == nil || invalid == nil {
			t.Fatal("expected immediate error outcomes")
		}
		requireEqual(t, resultText(unknown.Result), `Tool "echo" is unavailable`)
		requireEqual(t, unknown.Result.Details, nil)
		// upstream: packages/agent/src/harness/execution/tools.ts:88 uses JSON.stringify without HTML escaping.
		_, quotedName := execution.PrepareToolCall(&ai.ToolCall{Name: "<echo>&\""}, nil)
		if quotedName == nil {
			t.Fatal("quoted unknown tool did not produce an immediate outcome")
		}
		requireEqual(t, resultText(quotedName.Result), `Tool "<echo>&\"" is unavailable`)
		requireEqual(t, resultText(preparationFailure.Result), "cannot prepare")
		if got := resultText(invalid.Result); !strings.Contains(got, `Validation failed for tool "echo"`) {
			t.Fatalf("invalid argument result = %q", got)
		}
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:104
	t.Run("blocks calls and revalidates replacement arguments", func(t *testing.T) {
		prepared, immediate := execution.PrepareToolCall(toolCall(nil), []harness.AgentHarnessTool{echoTool()})
		if immediate != nil || prepared == nil {
			t.Fatal("expected prepared call")
		}
		_, blocked := execution.ApplyBeforeToolDecision(prepared, &execution.BeforeToolDecision{Block: &execution.ToolBlock{Reason: "denied", Terminate: true}})
		replaced, replacedImmediate := execution.ApplyBeforeToolDecision(prepared, &execution.BeforeToolDecision{Args: map[string]any{"value": "replacement"}})
		_, invalid := execution.ApplyBeforeToolDecision(prepared, &execution.BeforeToolDecision{Args: map[string]any{}})
		if blocked == nil {
			t.Fatal("expected immediate block")
		}
		requireEqual(t, blocked.Kind, "immediate")
		requireEqual(t, blocked.IsError, true)
		requireEqual(t, blocked.Terminate, true)
		requireEqual(t, resultText(blocked.Result), "denied")
		if replacedImmediate != nil || replaced == nil {
			t.Fatal("replacement was not cleared")
		}
		requireEqual(t, replaced.Args, map[string]any{"value": "replacement"})
		if invalid == nil {
			t.Fatal("invalid replacement was not immediate")
		}
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:120
	t.Run("executes with updates, passes the signal, and ignores late updates", func(t *testing.T) {
		gate := newEffectGate(t)
		var lateUpdate harness.AgentHarnessToolUpdateCallback
		calls := 0
		tool := echoTool()
		tool.Execute = func(ctx harness.Context, _ string, args map[string]any, onUpdate harness.AgentHarnessToolUpdateCallback, _ any, _ harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			calls++
			if ctx.Done() != gate.Signal().Done() {
				t.Error("tool context does not carry the gate signal identity")
			}
			onUpdate(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}, Details: map[string]any{"value": args["value"]}}, harness.AgentHarnessToolUpdateOptions{})
			lateUpdate = onUpdate
			return harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Details: map[string]any{"value": args["value"]}}, nil
		}
		cleared := clearedTool(t, tool)
		updates := []harness.AgentToolResult{}
		result, err := execution.ExecuteToolCall(harness.BackgroundContext(), cleared, gate, func(update harness.AgentToolResult, _ harness.AgentHarnessToolUpdateOptions) {
			updates = append(updates, update)
		}, nil, toolInvocation{})
		requireNoError(t, err)
		if lateUpdate != nil {
			lateUpdate(harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "late"}}, Details: map[string]any{"value": "late"}}, harness.AgentHarnessToolUpdateOptions{})
		}
		requireEqual(t, calls, 1)
		requireEqual(t, result.IsError, false)
		requireEqual(t, resultText(result.Result), "done")
		requireEqual(t, len(updates), 1)
		requireEqual(t, resultText(updates[0]), "partial")
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:151 (both it.each rows)
	for _, tc := range []struct {
		name    string
		execute func(harness.Context, string, map[string]any, harness.AgentHarnessToolUpdateCallback, any, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error)
	}{
		{"synchronous", func(harness.Context, string, map[string]any, harness.AgentHarnessToolUpdateCallback, any, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			panic(errors.New("tool failed"))
		}},
		{"asynchronous", func(harness.Context, string, map[string]any, harness.AgentHarnessToolUpdateCallback, any, harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			return harness.AgentToolResult{}, errors.New("tool failed")
		}},
	} {
		t.Run("converts "+tc.name+" tool throws to error output", func(t *testing.T) {
			tool := echoTool()
			tool.Execute = tc.execute
			result, err := execution.ExecuteToolCall(harness.BackgroundContext(), clearedTool(t, tool), newEffectGate(t), func(harness.AgentToolResult, harness.AgentHarnessToolUpdateOptions) {}, nil, toolInvocation{})
			requireNoError(t, err)
			requireEqual(t, result.IsError, true)
			requireEqual(t, resultText(result.Result), "tool failed")
		})
	}
	// upstream: packages/agent/test/harness/execution-tools.test.ts:173
	t.Run("lets abort-first gate refusal escape without invoking the tool", func(t *testing.T) {
		calls := 0
		tool := echoTool()
		execute := tool.Execute
		tool.Execute = func(ctx harness.Context, id string, args map[string]any, update harness.AgentHarnessToolUpdateCallback, toolContext any, invocation harness.AgentHarnessToolInvocation) (harness.AgentToolResult, error) {
			calls++
			return execute(ctx, id, args, update, toolContext, invocation)
		}
		cleared := clearedTool(t, tool)
		gate, control := execution.CreateGate()
		defer control.Close(errors.New("test cleanup"))
		control.BeginAbort(func() error { return nil })
		_, err := execution.ExecuteToolCall(harness.BackgroundContext(), cleared, gate, func(harness.AgentToolResult, harness.AgentHarnessToolUpdateOptions) {}, nil, toolInvocation{})
		if _, ok := err.(*execution.AbortRequested); !ok { //nolint:errorlint // Upstream asserts the thrown AbortRequested instance, not a wrapped cause.
			t.Fatalf("error = %v (%T), want AbortRequested", err, err)
		}
		requireEqual(t, calls, 0)
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:185
	t.Run("applies patches field by field and constructs the tool-result message", func(t *testing.T) {
		cleared := clearedTool(t, echoTool())
		originalUsage := ai.Usage{Input: 1, Output: 2, TotalTokens: 3}
		replacementUsage := originalUsage
		replacementUsage.Input, replacementUsage.TotalTokens = 5, 7
		executed := harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "original"}}, Details: map[string]any{"original": true}, Usage: &originalUsage}
		finalized := execution.FinalizeToolCall(cleared, execution.ExecutedToolCall{Result: executed, IsError: true}, &execution.AfterToolPatch{
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "patched"}}, Details: map[string]any{"patched": true}, Usage: &replacementUsage, IsError: new(false), Terminate: new(true),
		})
		before := time.Now().UnixMilli()
		message := execution.CreateToolResultMessage(finalized)
		requireEqual(t, finalized.IsError, false)
		requireEqual(t, finalized.Terminate, true)
		requireEqual(t, finalized.Result, harness.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "patched"}}, Details: map[string]any{"patched": true}, Usage: &replacementUsage, Terminate: new(true)})
		requireEqual(t, message, ai.ToolResultMessage{ToolCallID: "call-1", ToolName: "echo", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "patched"}}, Details: map[string]any{"patched": true}, Usage: &replacementUsage, IsError: false, Timestamp: message.Timestamp})
		encoded, err := json.Marshal(message)
		requireNoError(t, err)
		var fields map[string]any
		requireNoError(t, json.Unmarshal(encoded, &fields))
		requireEqual(t, fields["role"], "toolResult")
		if message.Timestamp < before {
			t.Fatalf("timestamp = %d, want >= %d", message.Timestamp, before)
		}
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:235
	t.Run("preserves unusual JSON object keys in tool-result details", func(t *testing.T) {
		var details map[string]any
		requireNoError(t, json.Unmarshal([]byte(`{"__proto__":{"preserved":true}}`), &details))
		finalized := execution.FinalizeToolCall(clearedTool(t, echoTool()), execution.ExecutedToolCall{Result: harness.AgentToolResult{Content: []ai.ToolResultMessageContent{}, Details: details}}, nil)
		message := execution.CreateToolResultMessage(finalized)
		got, ok := message.Details.(map[string]any)
		if !ok {
			t.Fatalf("details type = %T, want JSON object", message.Details)
		}
		if _, exists := got["__proto__"]; !exists {
			t.Fatal("details lost its own __proto__ property")
		}
		requireEqual(t, message.Details, details)
	})
	// upstream: packages/agent/test/harness/execution-tools.test.ts:245
	t.Run("normalizes missing content from untyped tools", func(t *testing.T) {
		finalized := execution.FinalizeToolCall(clearedTool(t, echoTool()), execution.ExecutedToolCall{Result: harness.AgentToolResult{Content: nil, Details: map[string]any{}}}, nil)
		requireEqual(t, execution.CreateToolResultMessage(finalized).Content, []ai.ToolResultMessageContent{})
	})
}
