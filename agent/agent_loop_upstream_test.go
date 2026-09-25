package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports of upstream packages/agent/test/agent-loop.test.ts. Each test names
// the upstream case it ports. PiG's agent owns the loop, so upstream's
// agentLoop(prompts, context, config, signal, streamFn) becomes Agent.Send (or
// runPrompt when a case replaces the loop's queue callbacks), and the stream
// function becomes a scriptedProvider.

// upstream: "should emit events with AgentMessage types"
func TestAgentLoop_EmitsEventsWithAgentMessageTypes(t *testing.T) {
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("Hi there!")}), EventCh: rec.ch})

	msgs := mustSend(t, a, "Hello")
	types := eventTypes(rec.stop())

	if got := roles(msgs); !reflect.DeepEqual(got, []string{"user", "assistant"}) {
		t.Fatalf("messages = %v, want [user assistant]", got)
	}
	for _, want := range []string{"agent_start", "turn_start", "message_start", "message_end", "turn_end", "agent_end"} {
		if !slices.Contains(types, want) {
			t.Fatalf("events %v lack %s", types, want)
		}
	}
}

// upstream: "should handle custom message types via convertToLlm". PiG's
// convertToLLM drops custom roles it does not know, as the upstream case's
// convertToLlm drops "notification".
func TestAgentLoop_HandlesCustomMessageTypesViaConvertToLlm(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("Response")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})
	a.SetMessages([]AgentMessage{{Custom: map[string]any{"role": "notification", "text": "This is a notification"}}})

	mustSend(t, a, "Hello")

	converted := provider.request(1).transcript.Messages()
	if len(converted) != 1 {
		t.Fatalf("converted messages = %d, want only the user message", len(converted))
	}
	if _, ok := converted[0].(ai.UserMessage); !ok {
		t.Fatalf("converted[0] = %T, want user", converted[0])
	}
}

// upstream: "should apply transformContext before convertToLlm"
func TestAgentLoop_AppliesTransformContextBeforeConvertToLlm(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("Response")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})
	a.SetMessages([]AgentMessage{userMessage("old message 1"), assistantText("old response 1"), userMessage("old message 2"), assistantText("old response 2")})
	var transformed []AgentMessage
	a.SetTransformContext(func(msgs []AgentMessage) []AgentMessage {
		transformed = msgs[len(msgs)-2:]
		return transformed
	})

	mustSend(t, a, "new message")

	if len(transformed) != 2 {
		t.Fatalf("transformed = %d messages, want 2", len(transformed))
	}
	if got := len(provider.request(1).transcript.Messages()); got != 2 {
		t.Fatalf("converted messages = %d, want the 2 transformed messages", got)
	}
}

// upstream: "should handle tool calls and results"
func TestAgentLoop_HandlesToolCallsAndResults(t *testing.T) {
	toolUsage := &ai.Usage{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, TotalTokens: 10}
	patchedUsage := &ai.Usage{Input: 5, Output: 6, CacheRead: 7, CacheWrite: 8, TotalTokens: 26}
	var executed []string
	tool := valueEchoTool(ToolModeParallel, func(value string) { executed = append(executed, value) })
	base := tool.execute
	tool.execute = func(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
		result, err := base(ctx, id, args, onUpdate)
		result.Usage = toolUsage
		return result, err
	}
	var observedUsage *ai.Usage
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}),
		Tools:   []AgentTool{tool},
		EventCh: rec.ch,
		AfterToolCall: []AfterToolCallHook{func(_ context.Context, _, _ string, _ json.RawMessage, result AgentToolResult) AfterToolCallResult {
			observedUsage = result.Usage
			return AfterToolCallResult{Usage: patchedUsage}
		}},
	})

	msgs := mustSend(t, a, "echo something")
	events := rec.stop()

	if !reflect.DeepEqual(executed, []string{"hello"}) {
		t.Fatalf("executed = %v, want [hello]", executed)
	}
	types := eventTypes(events)
	if !slices.Contains(types, "tool_execution_start") || !slices.Contains(types, "tool_execution_end") {
		t.Fatalf("events %v lack tool execution events", types)
	}
	for _, ev := range events {
		if end, ok := ev.(ToolExecutionEndEvent); ok && end.Result.IsError {
			t.Fatalf("tool_execution_end isError = true: %+v", end)
		}
	}
	if observedUsage != toolUsage {
		t.Fatalf("afterToolCall saw usage %+v, want the tool's %+v", observedUsage, toolUsage)
	}
	if got := findToolResult(t, msgs, "tool-1").Usage; got == nil || *got != *patchedUsage {
		t.Fatalf("tool result usage = %+v, want %+v", got, patchedUsage)
	}
}

// upstream: runLoop derives continuation from tool-call content, not a provider's
// stopReason label. A normal stop still executes the call; a length stop fails
// the truncated call without invoking it and continues so the model can retry.
// Error and aborted remain hard exits even if their partial content has a call.
func TestAgentLoop_ToolCallContentControlsContinuationAcrossStopReasons(t *testing.T) {
	tests := []struct {
		reason          ai.StopReason
		wantProvider    int
		wantExecuted    int
		wantToolResults int
		wantResultError bool
	}{
		{reason: ai.StopReasonStop, wantProvider: 2, wantExecuted: 1, wantToolResults: 1},
		{reason: ai.StopReasonLength, wantProvider: 2, wantToolResults: 1, wantResultError: true},
		{reason: ai.StopReasonError, wantProvider: 1},
		{reason: ai.StopReasonAborted, wantProvider: 1},
	}

	for _, test := range tests {
		t.Run(string(test.reason), func(t *testing.T) {
			executed := 0
			provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
				if call > 1 {
					return doneStream(textMessage("done"))
				}
				message := toolUseMessage(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))
				message.StopReason = test.reason
				if test.reason == ai.StopReasonError || test.reason == ai.StopReasonAborted {
					message.ErrorMessage = string(test.reason)
					return agentTestStream([]ai.AssistantMessageEvent{ai.ErrorEvent{Reason: test.reason, Error: message}})
				}
				return doneStream(message)
			}}
			a := NewAgent(AgentOptions{
				Model: scriptedModel(provider),
				Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(string) { executed++ })},
			})

			messages := mustSend(t, a, "run")

			var results []ToolResultMessage
			for _, message := range messages {
				if message.ToolResult != nil {
					results = append(results, *message.ToolResult)
				}
			}
			if provider.calls() != test.wantProvider || executed != test.wantExecuted || len(results) != test.wantToolResults {
				t.Fatalf("provider calls = %d, executed = %d, tool results = %d; want %d, %d, %d", provider.calls(), executed, len(results), test.wantProvider, test.wantExecuted, test.wantToolResults)
			}
			if len(results) > 0 && results[0].IsError != test.wantResultError {
				t.Fatalf("tool result isError = %v, want %v", results[0].IsError, test.wantResultError)
			}
		})
	}
}

// upstream: "should not execute tool calls from a length-truncated assistant message"
func TestAgentLoop_DoesNotExecuteToolCallsFromLengthTruncatedMessage(t *testing.T) {
	var executed []string
	truncated := toolUseMessage(toolCall("tool-1", "echo", ai.JsonObject{"value": "hel"}))
	truncated.StopReason = ai.StopReasonLength
	provider := &scriptedProvider{respond: func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call == 1 {
			return doneStream(truncated)
		}
		return doneStream(textMessage("done"))
	}}
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(v string) { executed = append(executed, v) })}, EventCh: rec.ch})

	msgs := mustSend(t, a, "echo something")
	events := rec.stop()

	if len(executed) != 0 {
		t.Fatalf("executed = %v, want nothing", executed)
	}
	var end *ToolExecutionEndEvent
	for _, ev := range events {
		if e, ok := ev.(ToolExecutionEndEvent); ok {
			end = &e
			break
		}
	}
	if end == nil || !end.Result.IsError || !strings.Contains(end.Result.Content, "output token limit") {
		t.Fatalf("tool_execution_end = %+v, want an output-token-limit error", end)
	}
	if provider.calls() != 2 {
		t.Fatalf("provider calls = %d, want 2 (the loop continues)", provider.calls())
	}
	if msgs[len(msgs)-1].Assistant == nil {
		t.Fatalf("last message role = %s, want assistant", msgs[len(msgs)-1].Role())
	}
}

// upstream: "should execute mutated beforeToolCall args without revalidation"
func TestAgentLoop_ExecutesMutatedBeforeToolCallArgsWithoutRevalidation(t *testing.T) {
	var executed []any
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		var decoded map[string]any
		_ = json.Unmarshal(args, &decoded)
		executed = append(executed, decoded["value"])
		return AgentToolResult{Content: "echoed"}, nil
	}}
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}),
		Tools: []AgentTool{tool},
		BeforeToolCall: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			return ToolCallHookResult{Args: json.RawMessage(`{"value":123}`)}
		}},
	})

	mustSend(t, a, "echo something")

	if !reflect.DeepEqual(executed, []any{float64(123)}) {
		t.Fatalf("executed = %v, want [123]", executed)
	}
}

// preparingEditTool is upstream's edit tool with prepareArguments.
type preparingEditTool struct {
	scriptTool
}

func (t *preparingEditTool) PrepareArguments(raw json.RawMessage) (json.RawMessage, error) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return raw, nil
	}
	oldText, okOld := input["oldText"].(string)
	newText, okNew := input["newText"].(string)
	if !okOld || !okNew {
		return raw, nil
	}
	edits, _ := input["edits"].([]any)
	edits = append(edits, map[string]any{"oldText": oldText, "newText": newText})
	return json.Marshal(map[string]any{"edits": edits})
}

// upstream: "should prepare tool arguments for validation"
func TestAgentLoop_PreparesToolArgumentsForValidation(t *testing.T) {
	replace := map[string]any{"type": "object", "properties": map[string]any{
		"oldText": map[string]any{"type": "string"}, "newText": map[string]any{"type": "string"},
	}, "required": []any{"oldText", "newText"}}
	var executed []string
	tool := &preparingEditTool{scriptTool{name: "edit", params: map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"edits": map[string]any{"type": "array", "items": replace}},
		"required":             []any{"edits"},
		"additionalProperties": false,
	}, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		executed = append(executed, string(args))
		return AgentToolResult{Content: "edited 1"}, nil
	}}}
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "edit", ai.JsonObject{"oldText": "before", "newText": "after"}))}),
		Tools: []AgentTool{tool},
	})

	mustSend(t, a, "edit something")

	if want := []string{`{"edits":[{"newText":"after","oldText":"before"}]}`}; !reflect.DeepEqual(executed, want) {
		t.Fatalf("executed = %v, want %v", executed, want)
	}
}

// upstream: "should emit tool_execution_end in completion order but persist tool results in source order"
func TestAgentLoop_EmitsToolExecutionEndInCompletionOrderButPersistsSourceOrder(t *testing.T) {
	var rec *eventRecorder
	var secondExecuted sync.Once
	secondRan := make(chan struct{})
	var parallelObserved, firstResolved bool
	var mu sync.Mutex
	tool := &scriptTool{name: "echo", params: valueSchema, mode: ToolModeParallel, execute: func(_ context.Context, id string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		value := argValue(args)
		if value == "first" {
			// Finish only after the second call's tool_execution_end was emitted.
			awaitSignal(t, secondRan, "second tool")
			rec.waitFor(t, func(ev AgentEvent) bool {
				end, ok := ev.(ToolExecutionEndEvent)
				return ok && end.ToolCallID == "tool-2"
			})
			mu.Lock()
			firstResolved = true
			mu.Unlock()
		}
		if value == "second" {
			mu.Lock()
			parallelObserved = !firstResolved
			mu.Unlock()
			secondExecuted.Do(func() { close(secondRan) })
		}
		return AgentToolResult{Content: "echoed: " + value}, nil
	}}
	rec = newEventRecorder(nil)
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
			toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
		)}),
		Tools:         []AgentTool{tool},
		ToolExecution: ToolModeParallel,
		EventCh:       rec.ch,
	})

	mustSend(t, a, "echo both")
	events := rec.stop()

	var endIDs, resultIDs, turnResultIDs []string
	for _, ev := range events {
		switch ev := ev.(type) {
		case ToolExecutionEndEvent:
			endIDs = append(endIDs, ev.ToolCallID)
		case MessageEndEvent:
			if ev.Message.ToolResult != nil {
				resultIDs = append(resultIDs, ev.Message.ToolResult.ToolCallID)
			}
		case TurnEndEvent:
			for _, result := range ev.ToolResults {
				turnResultIDs = append(turnResultIDs, result.ToolCallID)
			}
		}
	}
	if !parallelObserved {
		t.Fatal("the second call did not run while the first was pending")
	}
	if !reflect.DeepEqual(endIDs, []string{"tool-2", "tool-1"}) {
		t.Fatalf("tool_execution_end order = %v, want completion order [tool-2 tool-1]", endIDs)
	}
	if !reflect.DeepEqual(resultIDs, []string{"tool-1", "tool-2"}) {
		t.Fatalf("tool result order = %v, want source order", resultIDs)
	}
	if !reflect.DeepEqual(turnResultIDs, []string{"tool-1", "tool-2"}) {
		t.Fatalf("turn_end tool results = %v, want source order", turnResultIDs)
	}
}

// upstream: "should inject queued messages after all tool calls complete"
func TestAgentLoop_InjectsQueuedMessagesAfterAllToolCallsComplete(t *testing.T) {
	var a *Agent
	var executed []string
	tool := valueEchoTool(ToolModeParallel, func(value string) {
		executed = append(executed, value)
		if value == "first" {
			a.Steer(userMessage("interrupt"))
		}
	})
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	rec := newEventRecorder(nil)
	a = NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}, ToolExecution: ToolModeSequential, EventCh: rec.ch})

	mustSend(t, a, "start")
	events := rec.stop()

	if !reflect.DeepEqual(executed, []string{"first", "second"}) {
		t.Fatalf("executed = %v, want [first second]", executed)
	}
	var ends int
	var sequence []string
	for _, ev := range events {
		switch ev := ev.(type) {
		case ToolExecutionEndEvent:
			ends++
			if ev.Result.IsError {
				t.Fatalf("tool_execution_end isError: %+v", ev)
			}
		case MessageStartEvent:
			if ev.Message.ToolResult != nil {
				sequence = append(sequence, "tool:"+ev.Message.ToolResult.ToolCallID)
			} else if ev.Message.User != nil {
				sequence = append(sequence, ev.Message.User.Content[0].(ai.TextContent).Text)
			}
		}
	}
	if ends != 2 {
		t.Fatalf("tool_execution_end events = %d, want 2", ends)
	}
	interrupt := slices.Index(sequence, "interrupt")
	if interrupt < 0 || slices.Index(sequence, "tool:tool-1") > interrupt || slices.Index(sequence, "tool:tool-2") > interrupt {
		t.Fatalf("message sequence = %v, want the interrupt after both tool results", sequence)
	}
	if !slices.Contains(userTexts(provider.request(2).transcript), "interrupt") {
		t.Fatal("the second request lacks the interrupt message")
	}
}

// overlapProbe records whether a "second" call ran while the "first" call
// was still pending. The first call waits for the second call or a short
// timeout, so a sequential batch still completes.
type overlapProbe struct {
	mu              sync.Mutex
	firstResolved   bool
	overlapObserved bool
	secondRan       chan struct{}
	once            sync.Once
	order           []string
}

func newOverlapProbe() *overlapProbe { return &overlapProbe{secondRan: make(chan struct{})} }

func (p *overlapProbe) run(name, value string) {
	p.mu.Lock()
	p.order = append(p.order, name+":"+value)
	p.mu.Unlock()
	switch value {
	case "first", "a":
		select {
		case <-p.secondRan:
		case <-time.After(50 * time.Millisecond):
		}
		p.mu.Lock()
		p.firstResolved = true
		p.mu.Unlock()
	default:
		p.mu.Lock()
		p.overlapObserved = !p.firstResolved
		p.mu.Unlock()
		p.once.Do(func() { close(p.secondRan) })
	}
}

func (p *overlapProbe) tool(name string, mode ToolExecutionMode) *scriptTool {
	return &scriptTool{name: name, params: valueSchema, mode: mode, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		p.run(name, argValue(args))
		return AgentToolResult{Content: name + ": " + argValue(args)}, nil
	}}
}

// upstream: "should force sequential execution when a tool has executionMode=sequential even with default parallel config"
func TestAgentLoop_ForcesSequentialWhenToolIsSequentialUnderParallelConfig(t *testing.T) {
	probe := newOverlapProbe()
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("tool-1", "slow", ai.JsonObject{"value": "first"}),
			toolCall("tool-2", "slow", ai.JsonObject{"value": "second"}),
		)}),
		Tools: []AgentTool{probe.tool("slow", ToolModeSequential)},
	})

	msgs := mustSend(t, a, "run both")

	if probe.overlapObserved {
		t.Fatal("the second call started before the first finished")
	}
	var ids []string
	for _, m := range msgs {
		if m.ToolResult != nil {
			ids = append(ids, m.ToolResult.ToolCallID)
		}
	}
	if !reflect.DeepEqual(ids, []string{"tool-1", "tool-2"}) {
		t.Fatalf("tool results = %v, want [tool-1 tool-2]", ids)
	}
}

// upstream: "should force sequential execution when one of multiple tools has executionMode=sequential"
func TestAgentLoop_ForcesSequentialWhenOneOfMultipleToolsIsSequential(t *testing.T) {
	probe := newOverlapProbe()
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("tool-1", "slow", ai.JsonObject{"value": "a"}),
			toolCall("tool-2", "fast", ai.JsonObject{"value": "b"}),
		)}),
		// fast leaves its mode empty: upstream's default, which is parallel.
		Tools: []AgentTool{probe.tool("slow", ToolModeSequential), probe.tool("fast", "")},
	})

	mustSend(t, a, "run both")

	if !reflect.DeepEqual(probe.order, []string{"slow:a", "fast:b"}) || probe.overlapObserved {
		t.Fatalf("execution order = %v (overlap %v), want slow:a to finish before fast:b", probe.order, probe.overlapObserved)
	}
}

// upstream: "should allow parallel execution when all tools have executionMode=parallel"
func TestAgentLoop_AllowsParallelWhenAllToolsAreParallel(t *testing.T) {
	probe := newOverlapProbe()
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
			toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
		)}),
		Tools: []AgentTool{probe.tool("echo", ToolModeParallel)},
	})

	mustSend(t, a, "echo both")

	if !probe.overlapObserved {
		t.Fatal("the second call did not start before the first finished")
	}
}

// upstream: "runs finishTurn after tool-result messages and before turn_end"
func TestAgentLoop_RunsFinishTurnAfterToolResultMessagesAndBeforeTurnEnd(t *testing.T) {
	var ordering orderLog
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: argValue(args), Terminate: true}, nil
	}}
	rec := newEventRecorder(func(ev AgentEvent) {
		if _, ok := ev.(TurnEndEvent); ok {
			ordering.add("turn_end")
		}
	})
	a := NewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}),
		Tools:   []AgentTool{tool},
		EventCh: rec.ch,
		// OnMessagePersist runs synchronously on message_end.
		OnMessagePersist: func(m AgentMessage) error {
			ordering.add("message_end:" + m.Role())
			return nil
		},
		FinishTurn: func(_ context.Context, turn AgentTurnContext) *AgentTurnDecision {
			ordering.add("finishTurn")
			if len(turn.ToolResults) != 1 {
				t.Errorf("toolResults = %d, want 1", len(turn.ToolResults))
			}
			if last := turn.Context[len(turn.Context)-1]; last.ToolResult == nil {
				t.Errorf("context ends with %s, want toolResult", last.Role())
			}
			return nil
		},
	})

	mustSend(t, a, "echo")
	rec.stop()

	got := ordering.list()
	if want := []string{"message_end:toolResult", "finishTurn", "turn_end"}; !reflect.DeepEqual(got[len(got)-3:], want) {
		t.Fatalf("ordering = %v, want suffix %v", got, want)
	}
}

// upstream: "runs finishTurn for a %s assistant before turn_end without changing the hard exit" (error, aborted)
func TestAgentLoop_RunsFinishTurnForErrorOrAbortedAssistantBeforeTurnEnd(t *testing.T) {
	for _, reason := range []ai.StopReason{ai.StopReasonError, ai.StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			var ordering orderLog
			provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return errorStream(reason) }}
			rec := newEventRecorder(func(ev AgentEvent) {
				if _, ok := ev.(TurnEndEvent); ok {
					ordering.add("turn_end")
				}
			})
			a := NewAgent(AgentOptions{
				Model:   scriptedModel(provider),
				EventCh: rec.ch,
				FinishTurn: func(_ context.Context, turn AgentTurnContext) *AgentTurnDecision {
					if turn.Message.StopReason != reason {
						t.Errorf("finishTurn message stopReason = %q, want %q", turn.Message.StopReason, reason)
					}
					ordering.add("finishTurn")
					return &AgentTurnDecision{Action: AgentTurnContinue}
				},
			})
			queues := &countingQueues{followUp: func(int) []AgentMessage { return []AgentMessage{userMessage("queued")} }}
			cfg := a.createLoopConfig(false)
			queues.install(&cfg)

			runPrompt(t, a, cfg, userMessage("run"))
			rec.stop()

			if got := ordering.list(); !reflect.DeepEqual(got, []string{"finishTurn", "turn_end"}) {
				t.Fatalf("ordering = %v, want [finishTurn turn_end]", got)
			}
			steering, followUp := queues.polls()
			if provider.calls() != 1 || steering != 1 || followUp != 0 {
				t.Fatalf("provider calls %d, steering polls %d, follow-up polls %d; want 1, 1, 0", provider.calls(), steering, followUp)
			}
		})
	}
}

func noopTool() *scriptTool {
	return &scriptTool{name: "noop", params: map[string]any{"type": "object"}, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: "done"}, nil
	}}
}

// upstream: "action:end skips queue polling and next-turn preparation"
func TestAgentLoop_ActionEndSkipsQueuePollingAndNextTurnPreparation(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		return doneStream(toolUseMessage(toolCall("tool-1", "noop", nil)))
	}}
	var prepareNextTurnCalls int
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{noopTool()},
		FinishTurn: func(context.Context, AgentTurnContext) *AgentTurnDecision {
			return &AgentTurnDecision{Action: AgentTurnEnd}
		},
		PrepareNextTurn: func(context.Context, PrepareNextTurnContext) *AgentLoopTurnUpdate {
			prepareNextTurnCalls++
			return nil
		},
	})
	queues := &countingQueues{followUp: func(int) []AgentMessage { return []AgentMessage{userMessage("queued")} }}
	cfg := a.createLoopConfig(false)
	queues.install(&cfg)

	runPrompt(t, a, cfg, userMessage("run"))

	steering, followUp := queues.polls()
	if provider.calls() != 1 || steering != 1 || followUp != 0 || prepareNextTurnCalls != 0 {
		t.Fatalf("provider %d, steering polls %d, follow-up polls %d, prepareNextTurn %d; want 1, 1, 0, 0",
			provider.calls(), steering, followUp, prepareNextTurnCalls)
	}
}

// continueOnce is a FinishTurn that asks for continuation on its first call.
func continueOnce(calls *int) FinishTurn {
	return func(context.Context, AgentTurnContext) *AgentTurnDecision {
		*calls++
		if *calls == 1 {
			return &AgentTurnDecision{Action: AgentTurnContinue}
		}
		return nil
	}
}

// upstream: "makes exactly one context-only request when no natural request satisfies continuation"
func TestAgentLoop_MakesExactlyOneContextOnlyRequestForContinuation(t *testing.T) {
	var finishCalls int
	provider := &scriptedProvider{respond: replyText("response")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), FinishTurn: continueOnce(&finishCalls)})

	mustSend(t, a, "run")

	if provider.calls() != 2 || finishCalls != 2 {
		t.Fatalf("provider calls %d, finishTurn calls %d; want 2, 2", provider.calls(), finishCalls)
	}
}

// upstream: "lets a natural tool-result request satisfy continuation"
func TestAgentLoop_NaturalToolResultRequestSatisfiesContinuation(t *testing.T) {
	var finishCalls int
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "noop", nil))}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{noopTool()}, FinishTurn: continueOnce(&finishCalls)})

	mustSend(t, a, "run")

	if provider.calls() != 2 || finishCalls != 2 {
		t.Fatalf("provider calls %d, finishTurn calls %d; want 2, 2", provider.calls(), finishCalls)
	}
}

// upstream: "lets a natural %s request satisfy continuation" (steering, follow-up)
func TestAgentLoop_NaturalQueuedRequestSatisfiesContinuation(t *testing.T) {
	for _, queueKind := range []string{"steering", "follow-up"} {
		t.Run(queueKind, func(t *testing.T) {
			var finishCalls int
			provider := &scriptedProvider{respond: replyText("done")}
			a := NewAgent(AgentOptions{Model: scriptedModel(provider), FinishTurn: continueOnce(&finishCalls)})
			queued := userMessage(queueKind)
			followUpDelivered := false
			queues := &countingQueues{
				steering: func(poll int) []AgentMessage {
					if queueKind == "steering" && poll == 2 {
						return []AgentMessage{queued}
					}
					return nil
				},
				followUp: func(int) []AgentMessage {
					if queueKind != "follow-up" || followUpDelivered {
						return nil
					}
					followUpDelivered = true
					return []AgentMessage{queued}
				},
			}
			cfg := a.createLoopConfig(false)
			queues.install(&cfg)

			runPrompt(t, a, cfg, userMessage("run"))

			if provider.calls() != 2 || finishCalls != 2 {
				t.Fatalf("provider calls %d, finishTurn calls %d; want 2, 2", provider.calls(), finishCalls)
			}
			if !slices.Contains(userTexts(provider.request(2).transcript), queueKind) {
				t.Fatalf("second request users = %v, want %s", userTexts(provider.request(2).transcript), queueKind)
			}
		})
	}
}

// upstream: "prepares the initial request after pending messages and can replace request state"
func TestAgentLoop_PreparesInitialRequestAfterPendingMessagesAndReplacesState(t *testing.T) {
	original := &scriptedProvider{respond: replyText("wrong model")}
	replacement := &scriptedProvider{id: "replacement", respond: replyText("done")}
	replacementModel := &ai.Model{ID: "replacement", DisplayName: "replacement", Provider: replacement}
	canonical := userMessage("canonical projection")
	steering := userMessage("steering")
	var completed []AgentMessage
	var prepareCalls int
	high := ai.ThinkingHigh
	a := NewAgent(AgentOptions{
		Model: scriptedModel(original),
		OnMessagePersist: func(m AgentMessage) error {
			completed = append(completed, m)
			return nil
		},
		PrepareRequest: func(_ context.Context, request PrepareRequestContext) *AgentRequestUpdate {
			prepareCalls++
			if !slices.ContainsFunc(completed, func(m AgentMessage) bool { return m.User == steering.User }) {
				t.Error("prepareRequest ran before the steering message_end")
			}
			if !slices.ContainsFunc(request.Context, func(m AgentMessage) bool { return m.User == steering.User }) {
				t.Error("prepareRequest context lacks the steering message")
			}
			return &AgentRequestUpdate{Context: []AgentMessage{canonical}, Model: replacementModel, ThinkingLevel: &high}
		},
	})
	steeringDelivered := false
	queues := &countingQueues{steering: func(int) []AgentMessage {
		if steeringDelivered {
			return nil
		}
		steeringDelivered = true
		return []AgentMessage{steering}
	}}
	cfg := a.createLoopConfig(false)
	queues.install(&cfg)

	runPrompt(t, a, cfg, userMessage("prompt"))

	if prepareCalls != 1 || original.calls() != 0 || replacement.calls() != 1 {
		t.Fatalf("prepareRequest %d, original model requests %d, replacement %d; want 1, 0, 1", prepareCalls, original.calls(), replacement.calls())
	}
	req := replacement.request(1)
	if got := userTexts(req.transcript); !reflect.DeepEqual(got, []string{"canonical projection"}) || len(req.transcript.Messages()) != 1 {
		t.Fatalf("request messages = %v, want only the canonical projection", got)
	}
	if req.opts.Thinking != ai.ThinkingHigh {
		t.Fatalf("request thinking = %q, want high", req.opts.Thinking)
	}
}

// upstream: "does not poll steering after prepareRequest"
func TestAgentLoop_DoesNotPollSteeringAfterPrepareRequest(t *testing.T) {
	late := userMessage("late steering")
	var queued []AgentMessage
	var included []bool
	provider := &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		included = append(included, slices.Contains(userTexts(req.transcript), "late steering"))
		return doneStream(textMessage("done"))
	}}
	var preparations int
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		PrepareRequest: func(context.Context, PrepareRequestContext) *AgentRequestUpdate {
			preparations++
			if preparations == 1 {
				queued = append(queued, late)
			}
			return nil
		},
	})
	queues := &countingQueues{steering: func(int) []AgentMessage {
		out := queued
		queued = nil
		return out
	}}
	cfg := a.createLoopConfig(false)
	queues.install(&cfg)

	runPrompt(t, a, cfg, userMessage("run"))

	steering, _ := queues.polls()
	if !reflect.DeepEqual(included, []bool{false, true}) || preparations != 2 || steering != 3 {
		// Startup, post-turn delivery, then the final natural-stop check.
		t.Fatalf("requests included steering %v, preparations %d, steering polls %d; want [false true], 2, 3", included, preparations, steering)
	}
}

// upstream: "should use prepareNextTurn snapshot before continuing". Upstream
// appends a system message; PiG's transcript has no system message variant,
// so the appended guidance is a user message.
func TestAgentLoop_UsesPrepareNextTurnSnapshotBeforeContinuing(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	var prepareCalls int
	prepared := false
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)},
		PrepareNextTurn: func(_ context.Context, turn PrepareNextTurnContext) *AgentLoopTurnUpdate {
			prepareCalls++
			if prepared {
				return nil
			}
			prepared = true
			return &AgentLoopTurnUpdate{Context: slices.Clone(turn.Context), Messages: []AgentMessage{userMessage("updated guidance")}}
		},
	})

	mustSend(t, a, "echo something")

	if provider.calls() != 2 || prepareCalls != 1 {
		t.Fatalf("provider calls %d, prepareNextTurn calls %d; want 2, 1", provider.calls(), prepareCalls)
	}
	if !slices.Contains(userTexts(provider.request(2).transcript), "updated guidance") {
		t.Fatal("the second request lacks the prepared message")
	}
}

// upstream: "picks up steering queued during prepareNextTurn before the next request"
func TestAgentLoop_PicksUpSteeringQueuedDuringPrepareNextTurn(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "noop", nil))}
	var a *Agent
	a = NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{noopTool()},
		PrepareNextTurn: func(context.Context, PrepareNextTurnContext) *AgentLoopTurnUpdate {
			a.Steer(userMessage("late steering"))
			return nil
		},
	})

	mustSend(t, a, "run")

	if provider.calls() != 2 || !slices.Contains(userTexts(provider.request(2).transcript), "late steering") {
		t.Fatalf("provider calls %d, second request users %v; want 2 with the late steering", provider.calls(), userTexts(provider.request(2).transcript))
	}
}

// upstream: "action:end receives finalized turn context and stops before queue
// polling". Upstream's context and events start with the system message that
// declares the tool loadout; PiG's transcript does not carry system messages
// (reported gap), so the expected lists omit it.
func TestAgentLoop_ActionEndReceivesFinalizedTurnContext(t *testing.T) {
	var executed []string
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	var callbackToolResultIDs, callbackRoles []string
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{
		Model:   scriptedModel(provider),
		Tools:   []AgentTool{valueEchoTool(ToolModeParallel, func(v string) { executed = append(executed, v) })},
		EventCh: rec.ch,
		FinishTurn: func(_ context.Context, turn AgentTurnContext) *AgentTurnDecision {
			if turn.Message == nil {
				t.Error("finishTurn message is nil")
			}
			for _, result := range turn.ToolResults {
				callbackToolResultIDs = append(callbackToolResultIDs, result.ToolCallID)
			}
			callbackRoles = roles(turn.Context)
			return &AgentTurnDecision{Action: AgentTurnEnd}
		},
	})
	queues := &countingQueues{followUp: func(int) []AgentMessage { return []AgentMessage{userMessage("follow up should stay queued")} }}
	cfg := a.createLoopConfig(false)
	queues.install(&cfg)

	msgs := runPrompt(t, a, cfg, userMessage("echo something"))
	events := rec.stop()

	steering, followUp := queues.polls()
	if provider.calls() != 1 || !reflect.DeepEqual(executed, []string{"hello"}) || steering != 1 || followUp != 0 {
		t.Fatalf("provider %d, executed %v, steering polls %d, follow-up polls %d", provider.calls(), executed, steering, followUp)
	}
	if !reflect.DeepEqual(callbackToolResultIDs, []string{"tool-1"}) {
		t.Fatalf("finishTurn tool results = %v", callbackToolResultIDs)
	}
	wantRoles := []string{"system", "user", "assistant", "toolResult"}
	if !reflect.DeepEqual(callbackRoles, wantRoles) || !reflect.DeepEqual(roles(msgs), wantRoles) {
		t.Fatalf("finishTurn context %v, messages %v; want %v", callbackRoles, roles(msgs), wantRoles)
	}
	want := []string{
		"agent_start", "turn_start",
		"message_start", "message_end",
		"message_start", "message_end",
		"tool_execution_start", "tool_execution_end",
		"message_start", "message_end",
		"turn_end", "agent_end",
	}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v\nwant %v", got, want)
	}
}

func assistantCount(msgs []AgentMessage) int {
	n := 0
	for _, m := range msgs {
		if m.Assistant != nil {
			n++
		}
	}
	return n
}

// upstream: "should stop after a tool batch when every tool result sets terminate=true"
func TestAgentLoop_StopsAfterBatchWhenEveryToolResultTerminates(t *testing.T) {
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: "echoed: " + argValue(args), Terminate: true}, nil
	}}
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		return doneStream(toolUseMessage(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"})))
	}}
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}, EventCh: rec.ch})

	msgs := mustSend(t, a, "echo something")
	turnEnds := 0
	for _, name := range eventTypes(rec.stop()) {
		if name == "turn_end" {
			turnEnds++
		}
	}

	if provider.calls() != 1 || turnEnds != 1 || !reflect.DeepEqual(roles(msgs), []string{"system", "user", "assistant", "toolResult"}) {
		t.Fatalf("provider %d, turn_end %d, roles %v", provider.calls(), turnEnds, roles(msgs))
	}
}

// upstream: "should stop after a blocked tool call when beforeToolCall sets terminate=true"
func TestAgentLoop_StopsAfterBlockedToolCallWithTerminate(t *testing.T) {
	executed := false
	tool := &scriptTool{name: "echo", params: valueSchema, execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
		executed = true
		return AgentToolResult{Content: "should not execute"}, nil
	}}
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{tool},
		BeforeToolCall: []BeforeToolCallHook{func(context.Context, string, string, json.RawMessage) ToolCallHookResult {
			return ToolCallHookResult{Block: true, Reason: "Blocked by policy", Terminate: true}
		}},
	})

	msgs := mustSend(t, a, "echo something")

	result := findToolResult(t, msgs, "tool-1")
	if executed || provider.calls() != 1 || !result.IsError || result.Text() != "Blocked by policy" {
		t.Fatalf("executed %v, provider %d, result %+v", executed, provider.calls(), result)
	}
}

// upstream: "should continue after a mixed batch with one terminating blocked call"
func TestAgentLoop_ContinuesAfterMixedBatchWithOneTerminatingBlockedCall(t *testing.T) {
	var mu sync.Mutex
	var executed []string
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, func(v string) {
			mu.Lock()
			executed = append(executed, v)
			mu.Unlock()
		})},
		ToolExecution: ToolModeParallel,
		BeforeToolCall: []BeforeToolCallHook{func(_ context.Context, _, _ string, args json.RawMessage) ToolCallHookResult {
			if argValue(args) == "first" {
				return ToolCallHookResult{Block: true, Reason: "Blocked first", Terminate: true}
			}
			return ToolCallHookResult{}
		}},
	})

	mustSend(t, a, "echo both")

	if !reflect.DeepEqual(executed, []string{"second"}) || provider.calls() != 2 {
		t.Fatalf("executed %v, provider calls %d; want [second], 2", executed, provider.calls())
	}
}

// upstream: "should continue after parallel tool calls when not all tool results terminate"
func TestAgentLoop_ContinuesWhenNotAllParallelResultsTerminate(t *testing.T) {
	tool := &scriptTool{name: "echo", params: valueSchema, mode: ToolModeParallel, execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
		return AgentToolResult{Content: "echoed: " + argValue(args), Terminate: argValue(args) == "first"}, nil
	}}
	provider := &scriptedProvider{respond: toolCallsThenText(
		toolCall("tool-1", "echo", ai.JsonObject{"value": "first"}),
		toolCall("tool-2", "echo", ai.JsonObject{"value": "second"}),
	)}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{tool}, ToolExecution: ToolModeParallel})

	msgs := mustSend(t, a, "echo both")

	if want := []string{"system", "user", "assistant", "toolResult", "toolResult", "assistant"}; provider.calls() != 2 || !reflect.DeepEqual(roles(msgs), want) {
		t.Fatalf("provider %d, roles %v; want 2, %v", provider.calls(), roles(msgs), want)
	}
}

// upstream: "should allow afterToolCall to mark a tool batch as terminating"
func TestAgentLoop_AfterToolCallMarksBatchTerminating(t *testing.T) {
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		return doneStream(toolUseMessage(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"})))
	}}
	terminate := true
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)},
		AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			return AfterToolCallResult{Terminate: &terminate}
		}},
	})

	msgs := mustSend(t, a, "echo something")

	if provider.calls() != 1 || assistantCount(msgs) != 1 {
		t.Fatalf("provider calls %d, assistant turns %d; want 1, 1", provider.calls(), assistantCount(msgs))
	}
}

// upstream: agentLoopContinue "should throw when context has no messages".
// Agent.Continue reports it as ErrNoMessagesToContinue.
func TestAgentLoopContinue_ThrowsWhenContextHasNoMessages(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("unexpected")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})

	if _, err := a.Continue(context.Background()); !errors.Is(err, ErrNoMessagesToContinue) {
		t.Fatalf("Continue error = %v, want ErrNoMessagesToContinue", err)
	}
	if provider.calls() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls())
	}
}

// upstream: agentLoopContinue "should continue from existing context without
// emitting user message events". PiG's Continue returns the whole transcript,
// so the new messages are the ones past the existing context.
func TestAgentLoopContinue_ContinuesWithoutEmittingUserMessageEvents(t *testing.T) {
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("Response")}), EventCh: rec.ch})
	a.SetMessages([]AgentMessage{userMessage("Hello")})

	msgs, err := a.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}
	events := rec.stop()

	if got := roles(msgs[1:]); !reflect.DeepEqual(got, []string{"assistant"}) {
		t.Fatalf("new messages = %v, want [assistant]", got)
	}
	var ends []string
	for _, ev := range events {
		if end, ok := ev.(MessageEndEvent); ok {
			ends = append(ends, end.Message.Role())
		}
	}
	if !reflect.DeepEqual(ends, []string{"assistant"}) {
		t.Fatalf("message_end events = %v, want only the assistant", ends)
	}
}

// upstream: agentLoopContinue "should allow custom message types as last
// message (caller responsibility)". PiG's convertToLLM turns a "custom"
// message into a user message, as the upstream case's convertToLlm does.
func TestAgentLoopContinue_AllowsCustomMessageAsLastMessage(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("Response to custom message")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})
	a.SetMessages([]AgentMessage{{Custom: map[string]any{"role": RoleCustom, "content": "Hook content"}}})

	msgs, err := a.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}

	if got := roles(msgs[1:]); !reflect.DeepEqual(got, []string{"assistant"}) {
		t.Fatalf("new messages = %v, want [assistant]", got)
	}
	if got := userTexts(provider.request(1).transcript); !reflect.DeepEqual(got, []string{"Hook content"}) {
		t.Fatalf("request users = %v, want the custom message as user text", got)
	}
}
