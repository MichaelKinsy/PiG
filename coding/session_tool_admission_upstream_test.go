package coding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/5998-blocked-tool-terminate.test.ts:16.
func TestSessionBlockedToolTerminatesWithoutAnotherResponse(t *testing.T) {
	tool := &admissionTool{name: "echo", parameter: "text", label: "Echo", description: "Echo text back", forbidExecution: true}
	h := newRecoveryHarness(t, harnessOptions{
		tools: []agent.AgentTool{tool},
		extension: toolCallExtension(func(...any) (any, error) {
			return &extension.ToolCallEventResult{Block: true, Reason: "Blocked by terminating policy", Terminate: true}, nil
		}),
	}, admissionResponse("echo", "text", "hello"), fauxReply("should not run", ai.StopReasonStop, 0))
	messages, err := h.session.Send(t.Context(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if h.provider.callCount() != 1 || len(tool.executions()) != 0 {
		t.Fatalf("provider calls=%d, tool calls=%q", h.provider.callCount(), tool.executions())
	}
	results := toolResultMessages(messages)
	if len(results) != 1 || !results[0].IsError || results[0].Text() != "Blocked by terminating policy" {
		t.Fatalf("tool results = %+v", results)
	}
	var ends []agent.ToolExecutionEndEvent
	for _, event := range h.settle(t) {
		if end, ok := event.(agent.ToolExecutionEndEvent); ok {
			ends = append(ends, end)
		}
	}
	if len(ends) != 1 || !ends[0].Result.Terminate || !ends[0].Result.IsError {
		t.Fatalf("tool execution ends = %+v", ends)
	}
	for _, message := range messages {
		if message.Assistant != nil && assistantText(message.Assistant) == "should not run" {
			t.Fatal("the queued follow-up response was consumed")
		}
	}
	printAdmissionProbe(t, "blocked", []any{results[0].Text(), results[0].IsError, ends[0].Result.Terminate, h.provider.callCount()})
}

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/8935-parallel-preflight-abort.test.ts:16.
func TestSessionParallelPreflightAbortPreventsPreparedEffects(t *testing.T) {
	tool := &admissionTool{name: "external_write", parameter: "value", label: "External write", description: "Perform an external write"}
	var preflights, resultHooks []string
	ext := extension.Extension{Path: "/ext/preflight", Handlers: map[string][]extension.HandlerFn{
		"tool_call": {func(args ...any) (any, error) {
			event := args[0].(extension.CustomToolCallEvent)
			value := event.Input["value"].(string)
			preflights = append(preflights, value)
			if value == "second" {
				return nil, extension.FromContext(args[1].(context.Context)).Abort()
			}
			return nil, nil
		}},
		"tool_result": {func(args ...any) (any, error) {
			resultHooks = append(resultHooks, args[0].(extension.CustomToolResultEvent).ToolCallID)
			return nil, nil
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{tool}, extension: ext}, admissionResponse("external_write", "value", "first", "second"))
	messages, err := h.session.Send(t.Context(), "run both writes")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(preflights, []string{"first", "second"}) || len(tool.executions()) != 0 || len(resultHooks) != 0 {
		t.Fatalf("preflights=%q executions=%q result hooks=%q", preflights, tool.executions(), resultHooks)
	}
	var starts, ends []string
	for _, event := range h.settle(t) {
		switch e := event.(type) {
		case agent.ToolExecutionStartEvent:
			starts = append(starts, e.ToolCallID)
		case agent.ToolExecutionEndEvent:
			ends = append(ends, e.ToolCallID)
			if !e.Result.IsError {
				t.Fatalf("non-error tool end: %+v", e)
			}
		}
	}
	if len(starts) != len(preflights) {
		t.Fatalf("starts=%q preflights=%q", starts, preflights)
	}
	sortedStarts := slices.Sorted(slices.Values(starts))
	slices.Sort(ends)
	if !slices.Equal(ends, sortedStarts) {
		t.Fatalf("ends=%q starts=%q", ends, starts)
	}
	results := toolResultMessages(messages)
	if len(results) != len(starts) {
		t.Fatalf("results=%+v starts=%q", results, starts)
	}
	var texts []string
	for index, result := range results {
		texts = append(texts, result.Text())
		if result.ToolCallID != starts[index] || !result.IsError || result.Text() != "Operation aborted" {
			t.Fatalf("result %d = %+v, want %s aborted", index, result, starts[index])
		}
	}
	printAdmissionProbe(t, "abort", []any{preflights, append([]string{}, tool.executions()...), append([]string{}, resultHooks...), texts})
}

func TestSessionAbortBindingFollowsReplacementAndKeepsCloneOwner(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{extension: toolCallExtension(func(...any) (any, error) { return nil, nil })})
	appendAsst(t, h.session, "saved reply")
	clone, err := h.session.Clone()
	if err != nil {
		t.Fatal(err)
	}
	drainSessionEvents(t, clone)
	for _, replace := range []bool{false, true} {
		if replace {
			h.session.ReplaceRunner(inproc.NewRunner(nil, t.TempDir()))
		}
		runCtx, finish := h.session.beginAgentRun(t.Context())
		if err := h.session.currentRunner().CreateCommandContext().Abort(); err != nil {
			finish()
			t.Fatal(err)
		}
		cancelled := runCtx.Err() != nil
		finish()
		if !cancelled {
			t.Fatalf("source Session not cancelled after clone; replaced=%t", replace)
		}
	}
}

func printAdmissionProbe(t *testing.T, name string, value []any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("TOOL_ADMISSION %s %s\n", name, data)
}

type admissionTool struct {
	name            string
	parameter       string
	label           string
	description     string
	forbidExecution bool
	mu              sync.Mutex
	calls           []string
}

func (t *admissionTool) Name() string                           { return t.name }
func (t *admissionTool) Label() string                          { return t.label }
func (t *admissionTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (t *admissionTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Description: t.description, Parameters: map[string]any{
		"type": "object", "properties": map[string]any{t.parameter: map[string]any{"type": "string"}}, "required": []string{t.parameter},
	}}
}
func (t *admissionTool) Execute(_ context.Context, _ string, args json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var values map[string]string
	if err := json.Unmarshal(args, &values); err != nil {
		return agent.AgentToolResult{}, err
	}
	value := values[t.parameter]
	t.calls = append(t.calls, value)
	if t.forbidExecution {
		return agent.AgentToolResult{}, errors.New("tool should have been blocked")
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: value}}, Details: map[string]any{"value": value}}, nil
}
func (t *admissionTool) executions() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.calls)
}
func admissionResponse(name, parameter string, values ...string) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		message := &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse}
		for _, value := range values {
			message.Content = append(message.Content, ai.ToolCall{ID: "call-" + value, Name: name, Arguments: ai.JsonObject{parameter: value}})
		}
		return message
	}
}
