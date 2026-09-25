package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports of upstream packages/agent/test/agent.test.ts. Each test names the
// upstream case it ports. upstream subscribe() listeners become an EventCh
// consumer (eventRecorder), and an AbortSignal becomes the run's context.

// upstream: "should create an agent instance with default state"
func TestAgent_CreatesAgentWithDefaultState(t *testing.T) {
	a := NewAgent(AgentOptions{})

	if len(a.Tools()) != 0 || len(a.Messages()) != 0 || a.IsStreaming() || len(a.PeekQueuedMessages()) != 0 {
		t.Fatalf("tools %v, messages %v, streaming %v, queued %v; want empty idle state",
			a.Tools(), a.Messages(), a.IsStreaming(), a.PeekQueuedMessages())
	}
}

// upstream: "should create an agent instance with custom initial state".
func TestAgent_CreatesAgentWithCustomInitialState(t *testing.T) {
	model := scriptedModel(&scriptedProvider{respond: replyText("unused")})
	a := NewAgent(AgentOptions{SystemPrompt: "You are a helpful assistant.", Model: model, ThinkingLevel: ai.ThinkingLow})

	if a.SystemPrompt() != "You are a helpful assistant." || a.Model() != model || a.ThinkingLevel() != ai.ThinkingLow {
		t.Fatalf("prompt %q, model %v, thinking %q", a.SystemPrompt(), a.Model(), a.ThinkingLevel())
	}
}

// upstream: "should subscribe to events": state mutators emit no events.
func TestAgent_SubscribesToEvents(t *testing.T) {
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{EventCh: rec.ch})

	a.SetThinkingLevel(ai.ThinkingLow)
	a.SetThinkingLevel(ai.ThinkingHigh)

	if events := rec.stop(); len(events) != 0 || a.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("events %v, thinking %q; want no events and high", events, a.ThinkingLevel())
	}
}

// upstream: "emits full lifecycle events for thrown run failures"
func TestAgent_EmitsFullLifecycleEventsForThrownRunFailures(t *testing.T) {
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{Model: fakeTestModel(&recordingProvider{err: errors.New("provider exploded")}), EventCh: rec.ch})

	msgs := mustSend(t, a, "hello")

	want := []string{"agent_start", "turn_start", "message_start", "message_end", "message_start", "message_end", "turn_end", "agent_end"}
	if got := eventTypes(rec.stop()); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	last := msgs[len(msgs)-1].Assistant
	if last == nil || last.StopReason != ai.StopReasonError || last.ErrorMessage != "provider exploded" {
		t.Fatalf("last message = %+v, want the error assistant", msgs[len(msgs)-1])
	}
}

// upstream: "should await async subscribers before prompt resolves". Emit
// completes only when the EventCh consumer takes the event, so a consumer
// still busy with the turn's last events holds the run (and agent_end) open.
func TestAgent_AwaitsSubscribersBeforePromptResolves(t *testing.T) {
	barrier := make(chan struct{})
	listenerFinished := make(chan struct{})
	rec := newEventRecorder(func(ev AgentEvent) {
		if _, ok := ev.(TurnEndEvent); ok {
			<-barrier
			close(listenerFinished)
		}
	})
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("ok")}), EventCh: rec.ch})

	resolved := sendAsync(t, a, "hello")
	time.Sleep(10 * time.Millisecond)
	select {
	case <-resolved:
		t.Fatal("prompt resolved before the listener finished")
	default:
	}
	if !a.IsStreaming() {
		t.Fatal("agent is not streaming while a listener holds the run")
	}

	close(barrier)
	waitSignal(t, resolved, "prompt")
	waitSignal(t, listenerFinished, "listener")
	if a.IsStreaming() {
		t.Fatal("agent still streaming after the prompt resolved")
	}
	rec.stop()
}

// captureTool is upstream's delayed_tool/settled_tool: it keeps its onUpdate
// callback so the test can call it after the tool settles.
func captureTool(name string, captured chan<- ToolUpdateCallback, update bool) *scriptTool {
	return &scriptTool{name: name, mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(_ context.Context, _ string, _ json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
			captured <- onUpdate
			if update {
				onUpdate("running", map[string]any{"status": "running"})
			}
			return AgentToolResult{Content: "ok", Details: map[string]any{"status": "done"}, Terminate: true}, nil
		}}
}

// upstream: "should ignore tool updates after the tool execution settles"
func TestAgent_IgnoresToolUpdatesAfterToolExecutionSettles(t *testing.T) {
	captured := make(chan ToolUpdateCallback, 1)
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("call-1", "delayed_tool", nil))}),
		Tools:   []AgentTool{captureTool("delayed_tool", captured, true)},
		EventCh: rec.ch,
	})

	mustSend(t, a, "run tool")
	rec.waitFor(t, func(ev AgentEvent) bool { _, ok := ev.(AgentEndEvent); return ok })
	countAfterPrompt := len(rec.snapshot())
	(<-captured)("late", map[string]any{"status": "late"})
	time.Sleep(10 * time.Millisecond)
	events := rec.stop()

	updates := 0
	for _, ev := range events {
		if _, ok := ev.(ToolExecutionUpdateEvent); ok {
			updates++
		}
	}
	if updates != 1 || len(events) != countAfterPrompt {
		t.Fatalf("updates %d, events %d after the late update (were %d); want 1 and unchanged", updates, len(events), countAfterPrompt)
	}
}

// upstream: "should ignore a settled parallel tool update while another tool is still running"
func TestAgent_IgnoresSettledParallelToolUpdateWhileAnotherToolRuns(t *testing.T) {
	captured := make(chan ToolUpdateCallback, 1)
	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	slow := &scriptTool{name: "slow_tool", mode: ToolModeParallel, params: map[string]any{"type": "object"},
		execute: func(context.Context, string, json.RawMessage, ToolUpdateCallback) (AgentToolResult, error) {
			close(slowStarted)
			<-releaseSlow
			return AgentToolResult{Content: "done", Terminate: true}, nil
		}}
	rec := newEventRecorder(nil)
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(
			toolCall("call-1", "settled_tool", nil), toolCall("call-2", "slow_tool", nil))}),
		Tools:   []AgentTool{captureTool("settled_tool", captured, false), slow},
		EventCh: rec.ch,
	})

	done := sendAsync(t, a, "run tools")
	waitSignal(t, slowStarted, "slow tool")
	rec.waitFor(t, func(ev AgentEvent) bool {
		end, ok := ev.(TimingEvent)
		return ok && end.Kind == "tool" && end.Name == "settled_tool"
	})
	countBefore := len(rec.snapshot())
	(<-captured)("late", map[string]any{"status": "late"})
	time.Sleep(10 * time.Millisecond)
	if got := len(rec.snapshot()); got != countBefore {
		t.Fatalf("events %d after the late update, want %d", got, countBefore)
	}

	close(releaseSlow)
	waitSignal(t, done, "prompt")
	for _, ev := range rec.stop() {
		if _, ok := ev.(ToolExecutionUpdateEvent); ok {
			t.Fatalf("unexpected tool_execution_update %+v", ev)
		}
	}
}

// upstream: "should update state with mutators". Upstream's
// `state.messages.push` on the live array has no Go counterpart: Messages()
// returns the transcript slice, and appending to a slice never grows the
// agent's.
func TestAgent_UpdatesStateWithMutators(t *testing.T) {
	a := NewAgent(AgentOptions{})
	model := scriptedModel(&scriptedProvider{})
	a.SetModel(model)
	a.SetThinkingLevel(ai.ThinkingHigh)
	tools := []AgentTool{&scriptTool{name: "test"}}
	a.SetTools(tools)
	messages := []AgentMessage{userMessage("Hello")}
	a.SetMessages(messages)

	if a.Model() != model || a.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("model %v thinking %q", a.Model(), a.ThinkingLevel())
	}
	tools[0] = &scriptTool{name: "replaced"}
	messages[0] = assistantText("replaced")
	if a.Tools()[0].Name() != "test" || a.Messages()[0].User == nil {
		t.Fatal("SetTools/SetMessages kept the caller's array instead of a copy")
	}
	a.SetMessages(nil)
	if len(a.Messages()) != 0 {
		t.Fatalf("messages = %v after clearing", a.Messages())
	}
}

// upstream: "should support steering message queue"
func TestAgent_SupportsSteeringMessageQueue(t *testing.T) {
	a := NewAgent(AgentOptions{})
	a.Steer(userMessage("Steering message"))
	if len(a.Messages()) != 0 {
		t.Fatalf("steering message reached the transcript: %v", a.Messages())
	}
}

// upstream: "should support follow-up message queue"
func TestAgent_SupportsFollowUpMessageQueue(t *testing.T) {
	a := NewAgent(AgentOptions{})
	a.FollowUp(userMessage("Follow-up message"))
	if len(a.Messages()) != 0 {
		t.Fatalf("follow-up message reached the transcript: %v", a.Messages())
	}
}

// upstream: "should reject reset while processing without corrupting the transcript"
func TestAgent_RejectsResetWhileProcessing(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	provider := &scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream {
		stream := ai.NewAssistantMessageEventStream()
		_ = stream.Push(ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)})
		go func() {
			close(started)
			<-release
			_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: textMessage("Done")})
		}()
		return stream
	}}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider)})

	done := sendAsync(t, a, "Hello")
	waitSignal(t, started, "stream start")
	if !a.IsStreaming() || !reflect.DeepEqual(roles(a.Messages()), []string{"user"}) {
		t.Fatalf("streaming %v, roles %v; want streaming with [user]", a.IsStreaming(), roles(a.Messages()))
	}
	if err := a.Reset(); !errors.Is(err, ErrAlreadyProcessing) {
		t.Fatalf("Reset error = %v, want ErrAlreadyProcessing", err)
	}
	if !a.IsStreaming() || !reflect.DeepEqual(roles(a.Messages()), []string{"user"}) {
		t.Fatalf("after Reset: streaming %v, roles %v", a.IsStreaming(), roles(a.Messages()))
	}
	close(release)
	waitSignal(t, done, "prompt")

	if a.IsStreaming() || !reflect.DeepEqual(roles(a.Messages()), []string{"user", "assistant"}) {
		t.Fatalf("after the run: streaming %v, roles %v", a.IsStreaming(), roles(a.Messages()))
	}
}

// busyAgent starts a prompt whose response ends only when its context is
// cancelled, and returns the cancel and wait functions.
func busyAgent(t *testing.T) (*Agent, func()) {
	t.Helper()
	started := make(chan struct{}, 1)
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		return abortableStream(req.ctx, started)
	}})})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = a.Send(ctx, "First message")
	}()
	waitSignal(t, started, "stream start")
	return a, func() {
		cancel()
		waitSignal(t, done, "first prompt")
	}
}

// upstream: "should throw when prompt() called while streaming"
func TestAgent_ThrowsWhenPromptCalledWhileStreaming(t *testing.T) {
	a, stop := busyAgent(t)
	defer stop()

	if !a.IsStreaming() {
		t.Fatal("agent is not streaming")
	}
	if _, err := a.Send(context.Background(), "Second message"); !errors.Is(err, ErrAlreadyProcessingPrompt) {
		t.Fatalf("second Send error = %v, want ErrAlreadyProcessingPrompt", err)
	}
}

// upstream: "should throw when continue() called while streaming"
func TestAgent_ThrowsWhenContinueCalledWhileStreaming(t *testing.T) {
	a, stop := busyAgent(t)
	defer stop()

	if _, err := a.Continue(context.Background()); !errors.Is(err, ErrAlreadyProcessing) {
		t.Fatalf("Continue error = %v, want ErrAlreadyProcessing", err)
	}
}

// upstream: "continue() should process queued follow-up messages after an assistant turn"
func TestAgent_ContinueProcessesQueuedFollowUpAfterAssistantTurn(t *testing.T) {
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("Processed")})})
	a.SetMessages([]AgentMessage{userMessage("Initial"), assistantText("Initial response")})
	a.FollowUp(userMessage("Queued follow-up"))

	msgs, err := a.Continue(context.Background())
	if err != nil {
		t.Fatalf("Continue: %v", err)
	}

	hasFollowUp := slices.ContainsFunc(msgs, func(m AgentMessage) bool {
		return m.User != nil && m.User.Content[0].(ai.TextContent).Text == "Queued follow-up"
	})
	if !hasFollowUp || msgs[len(msgs)-1].Assistant == nil {
		t.Fatalf("roles %v, follow-up present %v; want the follow-up answered", roles(msgs), hasFollowUp)
	}
}

// recordUsers returns a provider that records each request's user texts.
func recordUsers(requests *[][]string) *scriptedProvider {
	return &scriptedProvider{respond: func(_ int, req scriptedRequest) *ai.AssistantMessageEventStream {
		*requests = append(*requests, userTexts(req.transcript))
		return doneStream(textMessage("done"))
	}}
}

// checkSteeringRequests asserts upstream's one-at-a-time/all request split
// for the "first"/"second" steering pair.
func checkSteeringRequests(t *testing.T, mode QueueMode, requests [][]string, first, second string) {
	t.Helper()
	want := 1
	if mode == QueueModeOneAtATime {
		want = 2
	}
	if len(requests) != want || !slices.Contains(requests[0], first) {
		t.Fatalf("requests = %v, want %d starting with %q", requests, want, first)
	}
	if mode == QueueModeOneAtATime && (slices.Contains(requests[0], second) || !slices.Contains(requests[1], second)) {
		t.Fatalf("requests = %v, want %q only in the second request", requests, second)
	}
	if mode == QueueModeAll && !slices.Contains(requests[0], second) {
		t.Fatalf("requests = %v, want %q in the first request", requests, second)
	}
}

// upstream: "continue() keeps $mode steering semantics for assistant-tail fallback"
func TestAgent_ContinueKeepsSteeringModeForAssistantTailFallback(t *testing.T) {
	for _, mode := range []QueueMode{QueueModeOneAtATime, QueueModeAll} {
		t.Run(string(mode), func(t *testing.T) {
			var requests [][]string
			a := NewAgent(AgentOptions{Model: scriptedModel(recordUsers(&requests)), SteeringMode: mode})
			a.SetMessages([]AgentMessage{userMessage("Initial"), assistantText("Initial response")})
			a.Steer(userMessage("Steering 1"))
			a.Steer(userMessage("Steering 2"))

			if _, err := a.Continue(context.Background()); err != nil {
				t.Fatalf("Continue: %v", err)
			}
			checkSteeringRequests(t, mode, requests, "Steering 1", "Steering 2")
		})
	}
}

// upstream: "keeps legacy prepareNextTurn signal callback behavior": the hook
// receives the run's context in place of the abort signal.
func TestAgent_KeepsPrepareNextTurnSignalCallbackBehavior(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "noop", nil))}
	sawContext := false
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{noopTool()},
		PrepareNextTurn: func(ctx context.Context, _ PrepareNextTurnContext) *AgentLoopTurnUpdate {
			sawContext = ctx != nil && ctx.Done() != nil
			return nil
		},
	})
	ctx := t.Context()

	if _, err := a.Send(ctx, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if provider.calls() != 2 || !sawContext {
		t.Fatalf("requests %d, saw run context %v; want 2, true", provider.calls(), sawContext)
	}
}

// upstream: "forwards finishTurn through AgentOptions with the active abort
// signal". Upstream's context starts with the tool-loadout system message,
// which PiG's transcript does not carry (reported gap).
func TestAgent_ForwardsFinishTurnWithActiveRunContext(t *testing.T) {
	provider := &scriptedProvider{respond: toolCallsThenText(toolCall("tool-1", "noop", nil))}
	ctx := t.Context()
	var sawRunContext bool
	var contextRoles []string
	a := NewAgent(AgentOptions{
		Model: scriptedModel(provider),
		Tools: []AgentTool{noopTool()},
		FinishTurn: func(turnCtx context.Context, turn AgentTurnContext) *AgentTurnDecision {
			sawRunContext = turnCtx == ctx
			contextRoles = roles(turn.Context)
			return &AgentTurnDecision{Action: AgentTurnEnd}
		},
	})

	if _, err := a.Send(ctx, "start"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if provider.calls() != 1 || !sawRunContext || !reflect.DeepEqual(contextRoles, []string{"system", "user", "assistant", "toolResult"}) {
		t.Fatalf("requests %d, run context %v, roles %v", provider.calls(), sawRunContext, contextRoles)
	}
}

// upstream: "rejects a queued continuation from $name context without
// draining queues" (empty). The system-only case needs transcript system
// messages, which PiG does not model (reported gap).
func TestAgent_RejectsQueuedContinuationFromEmptyContext(t *testing.T) {
	a := NewAgent(AgentOptions{Model: scriptedModel(&scriptedProvider{respond: replyText("unexpected")})})
	steering, followUp := userMessage("steering"), userMessage("follow-up")
	a.Steer(steering)
	a.FollowUp(followUp)

	if _, err := a.Continue(context.Background()); !errors.Is(err, ErrNoMessagesToContinue) {
		t.Fatalf("Continue error = %v, want ErrNoMessagesToContinue", err)
	}
	if got := a.PeekQueuedMessages(); len(got) != 1 || got[0].User != steering.User {
		t.Fatalf("queued = %v, want the steering message", got)
	}
	a.ClearSteeringQueue()
	if got := a.PeekQueuedMessages(); len(got) != 1 || got[0].User != followUp.User {
		t.Fatalf("queued = %v, want the follow-up message", got)
	}
}

// upstream: "defers follow-up input on the first continuation request from a $name tail" (user, toolResult)
func TestAgent_DefersFollowUpOnFirstContinuationRequest(t *testing.T) {
	toolResultTail := []AgentMessage{
		userMessage("existing user"),
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{toolCall("call-1", "noop", nil)}}},
		{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "call-1", ToolName: "noop", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "done"}}, Timestamp: 1}},
	}
	for name, messages := range map[string][]AgentMessage{"user": {userMessage("existing user")}, "toolResult": toolResultTail} {
		t.Run(name, func(t *testing.T) {
			var requests [][]string
			a := NewAgent(AgentOptions{Model: scriptedModel(recordUsers(&requests))})
			a.SetMessages(messages)
			a.FollowUp(userMessage("follow-up"))

			if _, err := a.Continue(context.Background()); err != nil {
				t.Fatalf("Continue: %v", err)
			}
			if len(requests) != 2 || slices.Contains(requests[0], "follow-up") || !slices.Contains(requests[1], "follow-up") {
				t.Fatalf("requests = %v, want the follow-up only in the second", requests)
			}
		})
	}
}

// upstream: "polls $mode steering at continuation startup"
func TestAgent_PollsSteeringAtContinuationStartup(t *testing.T) {
	for _, mode := range []QueueMode{QueueModeOneAtATime, QueueModeAll} {
		t.Run(string(mode), func(t *testing.T) {
			var requests [][]string
			a := NewAgent(AgentOptions{Model: scriptedModel(recordUsers(&requests)), SteeringMode: mode})
			a.SetMessages([]AgentMessage{userMessage("existing")})
			a.Steer(userMessage("first"))
			a.Steer(userMessage("second"))

			if _, err := a.Continue(context.Background()); err != nil {
				t.Fatalf("Continue: %v", err)
			}
			checkSteeringRequests(t, mode, requests, "first", "second")
		})
	}
}

// upstream: "keeps steering ahead of follow-up from a non-assistant continuation tail"
func TestAgent_KeepsSteeringAheadOfFollowUpFromNonAssistantTail(t *testing.T) {
	var requests [][]string
	a := NewAgent(AgentOptions{Model: scriptedModel(recordUsers(&requests))})
	a.SetMessages([]AgentMessage{userMessage("existing")})
	a.Steer(userMessage("steering"))
	a.FollowUp(userMessage("follow-up"))

	if _, err := a.Continue(context.Background()); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	if len(requests) != 2 || !slices.Contains(requests[0], "steering") || slices.Contains(requests[0], "follow-up") || !slices.Contains(requests[1], "follow-up") {
		t.Fatalf("requests = %v, want steering first and the follow-up second", requests)
	}
}

// steerOnAssistantEnd queues steering when the assistant message ends, as
// upstream's subscriber does.
func steerOnAssistantEnd(a **Agent, steering AgentMessage) *eventRecorder {
	return newEventRecorder(func(ev AgentEvent) {
		if end, ok := ev.(MessageEndEvent); ok && end.Message.Assistant != nil {
			(*a).Steer(steering)
		}
	})
}

func checkQueuesKept(t *testing.T, a *Agent, steering, followUp AgentMessage) {
	t.Helper()
	if got := a.PeekQueuedMessages(); len(got) != 1 || got[0].User != steering.User {
		t.Fatalf("queued = %v, want the steering message", roles(got))
	}
	a.ClearSteeringQueue()
	if got := a.PeekQueuedMessages(); len(got) != 1 || got[0].User != followUp.User {
		t.Fatalf("queued = %v, want the follow-up message", roles(got))
	}
}

// upstream: "keeps queues on a %s response even when finishTurn requests continuation" (error, aborted)
func TestAgent_KeepsQueuesOnFailedResponseDespiteContinuation(t *testing.T) {
	for _, reason := range []ai.StopReason{ai.StopReasonError, ai.StopReasonAborted} {
		t.Run(string(reason), func(t *testing.T) {
			steering, followUp := userMessage("steering"), userMessage("follow-up")
			var a *Agent
			rec := steerOnAssistantEnd(&a, steering)
			a = NewAgent(AgentOptions{
				Model:   scriptedModel(&scriptedProvider{respond: func(int, scriptedRequest) *ai.AssistantMessageEventStream { return errorStream(reason) }}),
				EventCh: rec.ch,
				FinishTurn: func(context.Context, AgentTurnContext) *AgentTurnDecision {
					return &AgentTurnDecision{Action: AgentTurnContinue}
				},
			})
			a.FollowUp(followUp)

			mustSend(t, a, "start")
			rec.stop()
			checkQueuesKept(t, a, steering, followUp)
		})
	}
}

// upstream: "keeps queues when finishTurn ends the run"
func TestAgent_KeepsQueuesWhenFinishTurnEndsTheRun(t *testing.T) {
	steering, followUp := userMessage("steering"), userMessage("follow-up")
	var a *Agent
	rec := steerOnAssistantEnd(&a, steering)
	a = NewAgent(AgentOptions{
		Model:   scriptedModel(&scriptedProvider{respond: replyText("done")}),
		EventCh: rec.ch,
		FinishTurn: func(context.Context, AgentTurnContext) *AgentTurnDecision {
			return &AgentTurnDecision{Action: AgentTurnEnd}
		},
	})
	a.FollowUp(followUp)

	mustSend(t, a, "start")
	rec.stop()
	checkQueuesKept(t, a, steering, followUp)
}

// upstream: "previews the next selected queued messages without consuming them"
func TestAgent_PreviewsNextSelectedQueuedMessagesWithoutConsuming(t *testing.T) {
	a := NewAgent(AgentOptions{SteeringMode: QueueModeOneAtATime, FollowUpMode: QueueModeAll})
	first, second, followUp := userMessage("first steering"), userMessage("second steering"), userMessage("follow-up")
	a.Steer(first)
	a.Steer(second)
	a.FollowUp(followUp)

	for range 2 {
		if got := a.PeekQueuedMessages(); len(got) != 1 || got[0].User != first.User {
			t.Fatalf("queued = %v, want only the first steering message", got)
		}
	}
	a.ClearSteeringQueue()
	if got := a.PeekQueuedMessages(); len(got) != 1 || got[0].User != followUp.User {
		t.Fatalf("queued = %v, want the follow-up", got)
	}
}

// upstream: "forwards sessionId to streamFunction options"
func TestAgent_ForwardsSessionIDToStreamOptions(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("ok")}
	a := NewAgent(AgentOptions{Model: scriptedModel(provider), SessionID: "session-abc"})

	mustSend(t, a, "hello")
	a.SetSessionID("session-def")
	mustSend(t, a, "hello again")

	if provider.request(1).opts.SessionID != "session-abc" || provider.request(2).opts.SessionID != "session-def" {
		t.Fatalf("session IDs = %q, %q", provider.request(1).opts.SessionID, provider.request(2).opts.SessionID)
	}
}
