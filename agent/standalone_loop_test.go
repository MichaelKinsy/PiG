package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Tests for upstream's standalone loop API (packages/agent/src/agent-loop.ts:
// agentLoop, agentLoopContinue, runAgentLoop, runAgentLoopContinue). Each case
// re-runs a scenario of packages/agent/test/agent-loop.test.ts through the
// exported functions instead of through Agent.

func standaloneConfig(provider *scriptedProvider) AgentLoopConfig {
	return AgentLoopConfig{Model: scriptedModel(provider)}
}

func collectLoopEvents(events *[]AgentEvent) AgentEventSink {
	return func(event AgentEvent) error {
		*events = append(*events, event)
		return nil
	}
}

// upstream: agent-loop.test.ts "should emit events with AgentMessage types".
func TestRunAgentLoop_EmitsEventsWithAgentMessageTypes(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("Hi there!")}
	var events []AgentEvent
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, standaloneConfig(provider), collectLoopEvents(&events), providerStream)
	if err != nil {
		t.Fatalf("RunAgentLoop: %v", err)
	}
	if got := roles(messages); !reflect.DeepEqual(got, []string{"user", "assistant"}) {
		t.Fatalf("messages = %v, want [user assistant]", got)
	}
	want := []string{"agent_start", "turn_start", "message_start", "message_end", "message_start", "message_end", "turn_end", "agent_end"}
	if got := eventTypes(events); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	end := events[len(events)-1].(AgentEndEvent)
	if !reflect.DeepEqual(roles(end.Messages), []string{"user", "assistant"}) {
		t.Fatalf("agent_end messages = %v", roles(end.Messages))
	}
}

// upstream: runAgentLoop returns only the new messages; the context's messages stay out of it
// and the caller's context is not mutated.
func TestRunAgentLoop_ReturnsOnlyNewMessagesAndLeavesTheContextAlone(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	prior := []AgentMessage{userMessage("earlier"), assistantText("reply")}
	agentContext := AgentContext{Messages: slices.Clone(prior)}
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("now")}, agentContext, standaloneConfig(provider), func(AgentEvent) error { return nil }, providerStream)
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(messages); !reflect.DeepEqual(got, []string{"user", "assistant"}) {
		t.Fatalf("returned messages = %v, want only the new ones", got)
	}
	if len(agentContext.Messages) != 2 || agentContext.Messages[0].User != prior[0].User {
		t.Fatalf("context was modified: %v", roles(agentContext.Messages))
	}
	if got := userTexts(provider.request(1).transcript); !reflect.DeepEqual(got, []string{"earlier", "now"}) {
		t.Fatalf("request users = %v, want the context then the prompt", got)
	}
}

// upstream: "should apply transformContext before convertToLlm".
func TestRunAgentLoop_AppliesTransformContextBeforeConvertToLlm(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	var order []string
	config.TransformContext = func(_ context.Context, messages []AgentMessage) ([]AgentMessage, error) {
		order = append(order, "transform")
		return messages[len(messages)-1:], nil
	}
	config.ConvertToLlm = func(messages []AgentMessage) ([]ai.Message, error) {
		order = append(order, "convert")
		if len(messages) != 1 {
			t.Errorf("convertToLlm saw %d messages, want the one transformContext kept", len(messages))
		}
		return ConvertToLLM(NormalizeMessages(messages, nil)), nil
	}
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("last")}, AgentContext{Messages: []AgentMessage{userMessage("old1"), userMessage("old2")}}, config, func(AgentEvent) error { return nil }, providerStream)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"transform", "convert"}) {
		t.Fatalf("order = %v", order)
	}
}

// upstream: "should handle tool calls and results" with a context tool, beforeToolCall and afterToolCall.
func TestRunAgentLoop_ExecutesContextToolsThroughTheHooks(t *testing.T) {
	var executed []string
	tool := valueEchoTool(ToolModeParallel, func(value string) { executed = append(executed, value) })
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "echo", ai.JsonObject{"value": "hello"}))}
	config := standaloneConfig(provider)
	var hooks []string
	config.BeforeToolCallHooks = []BeforeToolCallHook{func(_ context.Context, id, name string, _ json.RawMessage) ToolCallHookResult {
		hooks = append(hooks, "before "+id)
		return ToolCallHookResult{}
	}}
	config.AfterToolCallHooks = []AfterToolCallHook{func(_ context.Context, id, _ string, _ json.RawMessage, _ AgentToolResult) AfterToolCallResult {
		hooks = append(hooks, "after "+id)
		return AfterToolCallResult{}
	}}
	var events []AgentEvent
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("echo something")}, AgentContext{Tools: []AgentTool{tool}}, config, collectLoopEvents(&events), providerStream)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(executed, []string{"hello"}) || !reflect.DeepEqual(hooks, []string{"before tool-1", "after tool-1"}) {
		t.Fatalf("executed = %v hooks = %v", executed, hooks)
	}
	if got := roles(messages); !reflect.DeepEqual(got, []string{"system", "user", "assistant", "toolResult", "assistant"}) {
		t.Fatalf("messages = %v", got)
	}
	types := eventTypes(events)
	for _, want := range []string{"tool_execution_start", "tool_execution_end"} {
		if !slices.Contains(types, want) {
			t.Fatalf("events %v lack %s", types, want)
		}
	}
	// The tool declaration reaches the provider through the context's tools.
	var declared []string
	for _, message := range provider.request(1).transcript.Messages() {
		if system, ok := message.(ai.SystemMessage); ok {
			for _, tool := range system.ToolsAdded {
				declared = append(declared, tool.Name)
			}
		}
	}
	if !reflect.DeepEqual(declared, []string{"echo"}) {
		t.Fatalf("request declares tools %v, want [echo]", declared)
	}
}

// upstream: "should inject queued messages after all tool calls complete" and follow-up polling.
func TestRunAgentLoop_PollsSteeringAndFollowUpMessages(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	steering := [][]AgentMessage{{userMessage("steer")}}
	followUps := [][]AgentMessage{{userMessage("follow")}}
	config.GetSteeringMessages = func() []AgentMessage {
		if len(steering) == 0 {
			return nil
		}
		next := steering[0]
		steering = steering[1:]
		return next
	}
	config.GetFollowUpMessages = func() []AgentMessage {
		if len(followUps) == 0 {
			return nil
		}
		next := followUps[0]
		followUps = followUps[1:]
		return next
	}
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("start")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream)
	if err != nil {
		t.Fatal(err)
	}
	// Steering queued before the run joins the first request; the follow-up starts a second one.
	if got := roles(messages); !reflect.DeepEqual(got, []string{"user", "user", "assistant", "user", "assistant"}) {
		t.Fatalf("messages = %v", got)
	}
	if provider.calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls())
	}
}

// upstream: finishTurn "continue" makes exactly one context-only request.
func TestRunAgentLoop_FinishTurnCanRequestOneMoreRequest(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	var calls int
	config.FinishTurn = func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) {
		calls++
		if calls == 1 {
			return &AgentTurnDecision{Action: AgentTurnContinue}, nil
		}
		return nil, nil
	}
	if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
		t.Fatal(err)
	}
	if provider.calls() != 2 || calls != 2 {
		t.Fatalf("provider calls = %d, finishTurn calls = %d, want 2 and 2", provider.calls(), calls)
	}
}

// upstream: prepareNextTurn replaces the model and snapshot for the next request.
func TestRunAgentLoop_PrepareNextTurnReplacesTheModel(t *testing.T) {
	first := &scriptedProvider{id: "first", respond: toolCallsThenText(toolCall("t1", "echo", ai.JsonObject{"value": "x"}))}
	second := &scriptedProvider{id: "second", respond: replyText("second")}
	config := standaloneConfig(first)
	config.PrepareNextTurn = func(context.Context, PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
		return &AgentLoopTurnUpdate{Model: scriptedModel(second)}, nil
	}
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)}}, config, func(AgentEvent) error { return nil }, func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return model.Provider.Stream(ctx, transcript, options)
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.calls() != 1 || second.calls() != 1 {
		t.Fatalf("calls first=%d second=%d, want 1 and 1", first.calls(), second.calls())
	}
}

// upstream: runAgentLoop rejects when an awaited event listener throws, and no failed turn is synthesized.
func TestRunAgentLoop_PropagatesAnEventSinkFailure(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	boom := errors.New("sink failed")
	var seen []AgentEvent
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, standaloneConfig(provider), func(event AgentEvent) error {
		seen = append(seen, event)
		if _, ok := event.(MessageEndEvent); ok {
			return boom
		}
		return nil
	}, providerStream)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the sink failure", err)
	}
	if provider.calls() != 0 {
		t.Fatalf("provider calls = %d, want 0 after the failure", provider.calls())
	}
	for _, event := range seen {
		if end, ok := event.(AgentEndEvent); ok {
			t.Fatalf("a failed standalone run synthesized agent_end: %+v", end)
		}
	}
	_ = messages
}

// upstream: the sink's events are delivered in order and awaited before the loop continues.
func TestRunAgentLoop_AwaitsEachEventBeforeTheNext(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	var inSink atomic.Bool
	var overlapped atomic.Bool
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, standaloneConfig(provider), func(AgentEvent) error {
		if !inSink.CompareAndSwap(false, true) {
			overlapped.Store(true)
		}
		time.Sleep(time.Millisecond)
		inSink.Store(false)
		return nil
	}, providerStream)
	if err != nil || overlapped.Load() {
		t.Fatalf("err = %v, overlapped = %v", err, overlapped.Load())
	}
}

// upstream: runAgentLoop uses streamFn ?? getDefaultStreamFn(), which throws when none is configured.
func TestRunAgentLoop_NilStreamFunctionUsesTheConfiguredDefault(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	if _, err := GetDefaultStreamFn(); err == nil {
		t.Skip("a host default stream function is configured")
	}
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, standaloneConfig(provider), func(AgentEvent) error { return nil }, nil)
	if !errors.Is(err, ErrNoDefaultStreamFunction) {
		t.Fatalf("error = %v, want ErrNoDefaultStreamFunction", err)
	}
	if provider.calls() != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls())
	}
}

// Probed against Pi 1.0.4: runAgentLoop rejects when its streamFn throws, after the opening events and without a failed
// turn or agent_end; only the Agent turns that into a failed assistant turn.
func TestRunAgentLoop_StreamFunctionFailureRejectsTheRun(t *testing.T) {
	failing := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return nil, errors.New("no route")
	}
	var events []AgentEvent
	messages, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, standaloneConfig(&scriptedProvider{respond: replyText("x")}), collectLoopEvents(&events), failing)
	if err == nil || err.Error() != "no route" || messages != nil {
		t.Fatalf("RunAgentLoop = %v, %v; want the stream function's error", messages, err)
	}
	if got, want := eventTypes(events), []string{"agent_start", "turn_start", "message_start", "message_end"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Probed against Pi 1.0.4: a prepareNextTurn that throws rejects the run after the finished turn's turn_end.
func TestRunAgentLoop_PrepareNextTurnFailureRejectsTheRun(t *testing.T) {
	config := standaloneConfig(&scriptedProvider{respond: replyText("hi")})
	config.FinishTurn = func(context.Context, AgentTurnContext) (*AgentTurnDecision, error) {
		return &AgentTurnDecision{Action: AgentTurnContinue}, nil
	}
	config.PrepareNextTurn = func(context.Context, PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
		return nil, errors.New("prep failed")
	}
	var events []AgentEvent
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{}, config, collectLoopEvents(&events), providerStream)
	if err == nil || err.Error() != "prep failed" {
		t.Fatalf("error = %v, want the preparation failure", err)
	}
	if got, want := eventTypes(events), []string{"agent_start", "turn_start", "message_start", "message_end", "message_start", "message_end", "turn_end"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// upstream: a transformContext that rejects rejects runAgentLoop (agent-loop.ts streamAssistantResponse awaits it unguarded).
func TestRunAgentLoop_TransformContextFailureRejectsTheRun(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("x")}
	config := standaloneConfig(provider)
	config.TransformContext = func(context.Context, []AgentMessage) ([]AgentMessage, error) {
		return nil, errors.New("transform failed")
	}
	var events []AgentEvent
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{}, config, collectLoopEvents(&events), providerStream)
	if err == nil || err.Error() != "transform failed" || provider.calls() != 0 {
		t.Fatalf("error = %v, provider calls = %d; want the transform failure and no request", err, provider.calls())
	}
	if got := eventTypes(events); slices.Contains(got, "agent_end") || slices.Contains(got, "turn_end") {
		t.Fatalf("events = %v, want no synthesized turn", got)
	}
}

// Probed against Pi 1.0.4: `streamFn ?? getDefaultStreamFn()` is evaluated after the opening events, so a missing
// default rejects after agent_start, turn_start and the prompt's lifecycle (only agent_start and turn_start when continuing).
func TestRunAgentLoop_MissingDefaultStreamFunctionFailsAfterTheOpeningEvents(t *testing.T) {
	if _, err := GetDefaultStreamFn(); err == nil {
		t.Skip("a host default stream function is configured")
	}
	var events []AgentEvent
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{}, standaloneConfig(&scriptedProvider{}), collectLoopEvents(&events), nil)
	if !errors.Is(err, ErrNoDefaultStreamFunction) {
		t.Fatalf("error = %v", err)
	}
	if got, want := eventTypes(events), []string{"agent_start", "turn_start", "message_start", "message_end"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	events = nil
	_, err = RunAgentLoopContinue(context.Background(), AgentContext{Messages: []AgentMessage{userMessage("go")}}, standaloneConfig(&scriptedProvider{}), collectLoopEvents(&events), nil)
	if !errors.Is(err, ErrNoDefaultStreamFunction) {
		t.Fatalf("continue error = %v", err)
	}
	if got, want := eventTypes(events), []string{"agent_start", "turn_start"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("continue events = %v, want %v", got, want)
	}
}

func parallelEchoBatch() *scriptedProvider {
	return &scriptedProvider{respond: toolCallsThenText(
		toolCall("a", "echo", ai.JsonObject{"value": "1"}), toolCall("b", "echo", ai.JsonObject{"value": "2"}),
		toolCall("c", "echo", ai.JsonObject{"value": "3"}), toolCall("d", "echo", ai.JsonObject{"value": "4"}))}
}

// upstream: the sink is called for one event at a time even while parallel tool calls finish together; parallel calls
// must not run the caller's sink concurrently.
func TestRunAgentLoop_DeliversParallelToolEventsOneAtATime(t *testing.T) {
	var inSink, overlapped atomic.Bool
	var events []AgentEvent
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)}}, standaloneConfig(parallelEchoBatch()), func(event AgentEvent) error {
		if !inSink.CompareAndSwap(false, true) {
			overlapped.Store(true)
		}
		events = append(events, event)
		time.Sleep(time.Millisecond)
		inSink.Store(false)
		return nil
	}, providerStream)
	if err != nil || overlapped.Load() {
		t.Fatalf("err = %v, overlapped = %v", err, overlapped.Load())
	}
	if ends := strings.Count(strings.Join(eventTypes(events), " "), "tool_execution_end"); ends != 4 {
		t.Fatalf("tool_execution_end events = %d, want 4", ends)
	}
}

// upstream: a sink rejection while a parallel call emits tool_execution_end rejects Promise.all and the run. It must
// fail the run, not unwind the call's goroutine.
func TestRunAgentLoop_SinkFailureInAParallelToolCallFailsTheRun(t *testing.T) {
	boom := errors.New("sink down")
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)}}, standaloneConfig(parallelEchoBatch()), func(event AgentEvent) error {
		if _, ok := event.(ToolExecutionEndEvent); ok {
			return boom
		}
		return nil
	}, providerStream)
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want the sink failure", err)
	}
}

// upstream executePreparedToolCall awaits the update events, so a rejected tool_execution_update rejects the run
// (agent-loop.ts:833-845) instead of becoming the tool's error result.
func TestRunAgentLoop_ToolUpdateSinkFailureFailsTheRun(t *testing.T) {
	for _, mode := range []ToolExecutionMode{ToolModeSequential, ToolModeParallel} {
		tool := &scriptTool{name: "echo", mode: mode, params: valueSchema, execute: func(_ context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			onUpdate(AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}})
			return AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}}, nil
		}}
		provider := &scriptedProvider{respond: toolCallsThenText(toolCall("a", "echo", ai.JsonObject{"value": "1"}))}
		boom := errors.New("update rejected")
		var ended []ToolExecutionEndEvent
		_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{Tools: []AgentTool{tool}}, standaloneConfig(provider), func(event AgentEvent) error {
			switch event := event.(type) {
			case ToolExecutionUpdateEvent:
				return boom
			case ToolExecutionEndEvent:
				ended = append(ended, event)
			}
			return nil
		}, providerStream)
		if !errors.Is(err, boom) || len(ended) != 0 || provider.calls() != 1 {
			t.Fatalf("%s: error = %v, ended = %v, provider calls = %d; want the update failure before tool_execution_end", mode, err, ended, provider.calls())
		}
	}
}

// upstream: cancelling the signal ends the run with an aborted response.
func TestRunAgentLoop_CancellationAbortsTheRun(t *testing.T) {
	started := make(chan struct{}, 1)
	provider := &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		return abortableStream(req.ctx, started)
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var messages []AgentMessage
	var err error
	go func() {
		defer close(done)
		messages, err = RunAgentLoop(ctx, []AgentMessage{userMessage("hi")}, AgentContext{}, standaloneConfig(provider), func(AgentEvent) error { return nil }, providerStream)
	}()
	waitSignal(t, started, "the provider request")
	cancel()
	waitSignal(t, done, "the run to end")
	// Probed against Pi 1.0.4: an aborted run resolves with its messages; the abort is the last response.
	if err != nil {
		t.Fatalf("error = %v, want none", err)
	}
	last := messages[len(messages)-1]
	if last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonAborted {
		t.Fatalf("last message = %+v, want an aborted response", last)
	}
}

// upstream: agentLoop returns an EventStream that ends with agent_end and whose result is its messages.
func TestAgentLoop_StreamsEventsAndResolvesWithTheNewMessages(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("Hi there!")}
	stream := AgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, standaloneConfig(provider), providerStream)
	var types []string
	for event := range stream.Events() {
		types = append(types, eventType(event))
	}
	want := []string{"agent_start", "turn_start", "message_start", "message_end", "message_start", "message_end", "turn_end", "agent_end"}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("events = %v, want %v", types, want)
	}
	if messages := stream.Result(); !reflect.DeepEqual(roles(messages), []string{"user", "assistant"}) {
		t.Fatalf("result = %v", roles(messages))
	}
}

// upstream: the stream's result resolves without anyone iterating its events.
func TestAgentLoop_ResultResolvesWithoutConsumingEvents(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	stream := AgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, standaloneConfig(provider), providerStream)
	if messages := stream.Result(); len(messages) != 2 {
		t.Fatalf("result = %v", roles(messages))
	}
}

// upstream: agent-loop.ts agentLoop starts the run without a rejection handler: a failing run emits no agent_end and the failure is an unhandled rejection, not a result. Go ends the stream so a consumer does not wait forever (finishAgentEventStream); the error is available from RunAgentLoop.
func TestAgentLoop_AFailingRunEndsTheStreamWithoutAResult(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	config.ConvertToLlm = func([]AgentMessage) ([]ai.Message, error) { return nil, errors.New("convert failed") }
	stream := AgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, config, providerStream)
	var types []string
	for event := range stream.Events() {
		types = append(types, eventType(event))
	}
	for _, kind := range types {
		if kind == "agent_end" {
			t.Fatalf("a failed run emitted agent_end: %v", types)
		}
	}
	if result := stream.Result(); len(result) != 0 {
		t.Fatalf("result = %v, want none", roles(result))
	}
	if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err == nil || !strings.Contains(err.Error(), "convert failed") {
		t.Fatalf("RunAgentLoop error = %v", err)
	}
}

// upstream: agentLoopContinue "should throw when context has no messages".
func TestAgentLoopContinue_ThrowsWhenContextHasNoMessagesStandalone(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("unexpected")}
	if _, err := AgentLoopContinue(context.Background(), AgentContext{}, standaloneConfig(provider), providerStream); err == nil || err.Error() != "Cannot continue: no messages in context" {
		t.Fatalf("AgentLoopContinue error = %v", err)
	}
	if _, err := RunAgentLoopContinue(context.Background(), AgentContext{}, standaloneConfig(provider), func(AgentEvent) error { return nil }, providerStream); err == nil || err.Error() != "Cannot continue: no messages in context" {
		t.Fatalf("RunAgentLoopContinue error = %v", err)
	}
	if provider.calls() != 0 {
		t.Fatalf("provider calls = %d", provider.calls())
	}
}

// upstream: continuing from an assistant message throws before any event.
func TestAgentLoopContinue_ThrowsWhenTheLastMessageIsAnAssistant(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("unexpected")}
	agentContext := AgentContext{Messages: []AgentMessage{userMessage("hi"), assistantText("hello")}}
	var events []AgentEvent
	_, err := RunAgentLoopContinue(context.Background(), agentContext, standaloneConfig(provider), collectLoopEvents(&events), providerStream)
	if err == nil || err.Error() != "Cannot continue from message role: assistant" || len(events) != 0 {
		t.Fatalf("error = %v, events = %v", err, eventTypes(events))
	}
	if _, err := AgentLoopContinue(context.Background(), agentContext, standaloneConfig(provider), providerStream); err == nil || err.Error() != "Cannot continue from message role: assistant" {
		t.Fatalf("AgentLoopContinue error = %v", err)
	}
}

// upstream: "should continue from existing context without emitting user message events".
func TestAgentLoopContinue_ContinuesWithoutEmittingUserMessageEventsStandalone(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("Response")}
	var events []AgentEvent
	messages, err := RunAgentLoopContinue(context.Background(), AgentContext{Messages: []AgentMessage{userMessage("Hello")}}, standaloneConfig(provider), collectLoopEvents(&events), providerStream)
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(messages); !reflect.DeepEqual(got, []string{"assistant"}) {
		t.Fatalf("new messages = %v, want [assistant]", got)
	}
	var ends []string
	for _, event := range events {
		if end, ok := event.(MessageEndEvent); ok {
			ends = append(ends, end.Message.Role())
		}
	}
	if !reflect.DeepEqual(ends, []string{"assistant"}) {
		t.Fatalf("message_end events = %v, want only the assistant", ends)
	}
	stream, err := AgentLoopContinue(context.Background(), AgentContext{Messages: []AgentMessage{userMessage("Hello")}}, standaloneConfig(&scriptedProvider{respond: replyText("again")}), providerStream)
	if err != nil {
		t.Fatal(err)
	}
	if got := stream.Result(); !reflect.DeepEqual(roles(got), []string{"assistant"}) {
		t.Fatalf("stream result = %v", roles(got))
	}
}

// The Agent builds its runs from the same exported config: its tools, hooks, and
// queues come through createLoopConfig, so the standalone loop and Agent share one mechanism.
func TestAgentRunsThroughTheExportedLoopConfig(t *testing.T) {
	a := mustNewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")}), MaxTurns: 7, ToolExecution: ToolModeSequential})
	config := a.createLoopConfig(false)
	if config.Model != a.opts.Model || config.MaxTurns != 7 || config.ToolExecution != ToolModeSequential {
		t.Fatalf("createLoopConfig = %+v", config)
	}
}

// upstream: streamAssistantResponse resolves `config.getApiKey?.(config.model.provider) || config.apiKey`
// before every request, so an expiring token is read again for each call.
func TestRunAgentLoop_ResolvesTheAPIKeyForEveryRequestFromTheRequestedProvider(t *testing.T) {
	first := &scriptedProvider{id: "first", respond: toolCallsThenText(toolCall("t1", "echo", ai.JsonObject{"value": "x"}))}
	second := &scriptedProvider{id: "second", respond: replyText("done")}
	config := standaloneConfig(first)
	var asked []string
	config.GetAPIKey = func(provider string) (string, error) {
		asked = append(asked, provider)
		return "key-" + provider + "-" + string(rune('0'+len(asked))), nil
	}
	config.PrepareNextTurn = func(context.Context, PrepareNextTurnContext) (*AgentLoopTurnUpdate, error) {
		return &AgentLoopTurnUpdate{Model: scriptedModel(second)}, nil
	}
	viaModel := func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return model.Provider.Stream(ctx, transcript, options)
	}
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("go")}, AgentContext{Tools: []AgentTool{valueEchoTool(ToolModeParallel, nil)}}, config, func(AgentEvent) error { return nil }, viaModel)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asked, []string{"first", "second"}) {
		t.Fatalf("getApiKey asked for %v, want each request's provider", asked)
	}
	if got := first.request(1).opts.APIKey; got != "key-first-1" {
		t.Fatalf("first request key = %q", got)
	}
	if got := second.request(1).opts.APIKey; got != "key-second-2" {
		t.Fatalf("second request key = %q", got)
	}
}

// upstream: the static apiKey applies when getApiKey is absent or returns nothing.
func TestRunAgentLoop_FallsBackToTheStaticAPIKey(t *testing.T) {
	for name, resolver := range map[string]func(string) (string, error){
		"no resolver":         nil,
		"resolver finds none": func(string) (string, error) { return "", nil },
	} {
		t.Run(name, func(t *testing.T) {
			provider := &scriptedProvider{respond: replyText("ok")}
			config := standaloneConfig(provider)
			config.APIKey = "static"
			config.GetAPIKey = resolver
			if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
				t.Fatal(err)
			}
			if got := provider.request(1).opts.APIKey; got != "static" {
				t.Fatalf("request key = %q, want the static key", got)
			}
		})
	}
}

// upstream: a resolved key wins over the static one.
func TestRunAgentLoop_ResolvedAPIKeyWinsOverTheStaticKey(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	config.APIKey = "static"
	config.GetAPIKey = func(string) (string, error) { return "fresh", nil }
	if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
		t.Fatal(err)
	}
	if got := provider.request(1).opts.APIKey; got != "fresh" {
		t.Fatalf("request key = %q, want fresh", got)
	}
}

// upstream: Agent.createLoopConfig passes this.getApiKey, and the property is assignable.
func TestAgentPassesGetAPIKeyToItsRuns(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), GetAPIKey: func(provider string) (string, error) { return "initial-" + provider, nil }})
	mustSend(t, a, "one")
	if got := provider.request(1).opts.APIKey; got != "initial-scripted" {
		t.Fatalf("first run key = %q", got)
	}
	a.SetGetAPIKey(func(provider string) (string, error) { return "replaced-" + provider, nil })
	mustSend(t, a, "two")
	if got := provider.request(2).opts.APIKey; got != "replaced-scripted" {
		t.Fatalf("second run key = %q", got)
	}
	if a.GetAPIKeyFunc() == nil {
		t.Fatal("GetAPIKeyFunc returned nil after SetGetAPIKey")
	}
	a.SetGetAPIKey(nil)
	mustSend(t, a, "three")
	if got := provider.request(3).opts.APIKey; got != "" {
		t.Fatalf("run without a resolver key = %q, want none", got)
	}
}

// upstream: agentLoop starts `void runAgentLoop(...).then(...)` with no rejection handler, so a failing run is an
// unhandled rejection that Node reports on the process. PiG reports it on the process log and ends the stream with the messages produced so far, because a Go consumer of Events would otherwise wait forever.
func TestAgentLoop_ReportsARunFailureOnTheProcessLogAndEndsTheStream(t *testing.T) {
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	config := standaloneConfig(&scriptedProvider{respond: replyText("ok")})
	config.ConvertToLlm = func([]AgentMessage) ([]ai.Message, error) { return nil, errors.New("convert exploded") }
	stream := AgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, config, providerStream)
	for range stream.Events() {
	}
	if result := stream.Result(); len(result) != 0 {
		t.Fatalf("a run that failed at the start resolved with %v", roles(result))
	}
	if out := logged.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "convert exploded") {
		t.Fatalf("process log = %q, want the run failure", out)
	}

	logged.Reset()
	ok := AgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, standaloneConfig(&scriptedProvider{respond: replyText("fine")}), providerStream)
	if result := ok.Result(); len(result) != 2 || logged.Len() != 0 {
		t.Fatalf("successful run: result = %v, log = %q", roles(result), logged.String())
	}
	// A cancelled run ends with its aborted response and is not a failure.
	started := make(chan struct{}, 1)
	cancelled, cancel := context.WithCancel(context.Background())
	abortable := &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		return abortableStream(req.ctx, started)
	}}
	aborted := AgentLoop(cancelled, []AgentMessage{userMessage("Hello")}, AgentContext{}, standaloneConfig(abortable), providerStream)
	waitSignal(t, started, "the provider request")
	cancel()
	messages := aborted.Result()
	if last := messages[len(messages)-1]; last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonAborted {
		t.Fatalf("cancelled run: messages = %v, want the aborted response last", roles(messages))
	}
	if logged.Len() != 0 {
		t.Fatalf("a cancelled run was reported as a failure: %q", logged.String())
	}
	continued, err := AgentLoopContinue(context.Background(), AgentContext{Messages: []AgentMessage{userMessage("Hello")}}, func() AgentLoopConfig {
		c := standaloneConfig(&scriptedProvider{respond: replyText("x")})
		c.ConvertToLlm = func([]AgentMessage) ([]ai.Message, error) { return nil, errors.New("continue exploded") }
		return c
	}(), providerStream)
	if err != nil {
		t.Fatal(err)
	}
	_ = continued.Result()
	if out := logged.String(); !strings.Contains(out, "continue exploded") {
		t.Fatalf("process log after AgentLoopContinue = %q", out)
	}
}

// upstream: streamAssistantResponse awaits getApiKey without a catch, so a rejected resolution rejects runAgentLoop
// before the request, and the Agent ends the run with its failed-turn lifecycle (Agent.handleRunFailure).
func TestGetAPIKeyFailureFailsTheRun(t *testing.T) {
	refresh := errors.New("token refresh failed")
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	config.GetAPIKey = func(string) (string, error) { return "", refresh }
	var events []AgentEvent
	_, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, collectLoopEvents(&events), providerStream)
	if !errors.Is(err, refresh) || provider.calls() != 0 {
		t.Fatalf("standalone: error = %v, provider calls = %d; want the resolver failure and no request", err, provider.calls())
	}
	if got := eventTypes(events); slices.Contains(got, "agent_end") {
		t.Fatalf("standalone events = %v, want no synthesized end", got)
	}

	agentProvider := &scriptedProvider{respond: replyText("ok")}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(agentProvider), GetAPIKey: func(string) (string, error) { return "", refresh }})
	// Agent.prompt settles after handleRunFailure records the failed turn; the failure is the turn's error message.
	if _, err := a.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("Agent.Send error = %v", err)
	}
	if agentProvider.calls() != 0 {
		t.Fatalf("Agent provider calls = %d, want 0", agentProvider.calls())
	}
	last := a.Messages()[len(a.Messages())-1]
	if last.Assistant == nil || last.Assistant.StopReason != ai.StopReasonError || last.Assistant.ErrorMessage != refresh.Error() {
		t.Fatalf("Agent last message = %+v, want the failed turn", last)
	}
}

// upstream: AgentLoopConfig extends SimpleStreamOptions (packages/agent/src/types.ts), so each request option is a property of the
// config itself. The loop forwards the promoted options to every provider request.
func TestRunAgentLoop_ForwardsPromotedStreamOptionsToTheRequest(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	config := standaloneConfig(provider)
	config.MaxRetries = new(3)
	config.SessionID = "session-7"
	config.Transport = ai.TransportSSE
	config.CacheRetention = ai.CacheRetentionLong
	config.Metadata = map[string]any{"user_id": "u1"}
	if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
		t.Fatal(err)
	}
	got := provider.request(1).opts
	if got.MaxRetries == nil || *got.MaxRetries != 3 || got.SessionID != "session-7" || got.Transport != ai.TransportSSE || got.CacheRetention != ai.CacheRetentionLong || got.Metadata["user_id"] != "u1" {
		t.Fatalf("request options = %+v, want the config's promoted options", got)
	}
}

// .upstream/v1.1.0/packages/agent/test/agent-loop.test.ts:80 "uses the configured default when a legacy caller omits streamFn"
// (describe "default stream function compatibility"): a loop started without a stream function calls the host default once.
// Go has no caller that skips the argument, so the omission is a nil streamFn.
func TestAgentLoop_UsesTheConfiguredDefaultWhenAStreamFunctionIsOmitted(t *testing.T) {
	var calls atomic.Int32
	SetDefaultStreamFn(func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		calls.Add(1)
		return doneStream(textMessage("fallback")), nil
	})
	t.Cleanup(func() { SetDefaultStreamFn(nil) })

	stream := AgentLoop(context.Background(), []AgentMessage{userMessage("Hello")}, AgentContext{}, AgentLoopConfig{Model: &ai.Model{}}, nil)
	messages := stream.Result()
	if got := calls.Load(); got != 1 {
		t.Fatalf("default stream function calls = %d, want 1", got)
	}
	if got := roles(messages); !reflect.DeepEqual(got, []string{"user", "assistant"}) {
		t.Fatalf("messages = %v, want [user assistant]", got)
	}
}

// upstream: AgentLoopConfig extends SimpleStreamOptions, whose `toolChoice?: ToolChoice` ("auto" | "none", ai/src/types.ts:358) the loop spreads into
// every streamSimple request (agent-loop.ts streamAssistantResponse). The Go config carries it in the embedded StreamOptions.ToolChoice.
func TestRunAgentLoop_ForwardsToolChoiceToEveryRequest(t *testing.T) {
	for _, choice := range []any{nil, "auto", "none", ai.ToolChoiceNone} {
		provider := &scriptedProvider{respond: replyText("ok")}
		config := standaloneConfig(provider)
		config.ToolChoice = choice
		if _, err := RunAgentLoop(context.Background(), []AgentMessage{userMessage("hi")}, AgentContext{}, config, func(AgentEvent) error { return nil }, providerStream); err != nil {
			t.Fatal(err)
		}
		if got := provider.request(1).opts.ToolChoice; got != choice {
			t.Errorf("request toolChoice = %#v, want %#v", got, choice)
		}
	}
}
