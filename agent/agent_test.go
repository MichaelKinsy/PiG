package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestAgentForwardsThinkingBudgets(t *testing.T) {
	provider := &recordingProvider{seqs: [][]ai.AssistantMessageEvent{textSeq("")}}
	budgets := &ai.ThinkingBudgets{Medium: 4096}
	agent := NewAgent(AgentOptions{
		Model:           &ai.Model{ID: "model", Provider: provider},
		ThinkingBudgets: budgets,
	})
	if _, err := agent.Send(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	opts := provider.StreamOptions()
	if len(opts) != 1 || opts[0].ThinkingBudgets == nil || opts[0].ThinkingBudgets.Medium != 4096 {
		t.Fatalf("stream thinking budgets = %+v", opts)
	}
}

// Sending with no model selected (user not logged in / no model chosen) must
// surface an error, not dereference a nil *ai.Model in runLoop
// (currentModel.Capabilities / currentModel.Provider). Mirrors upstream
// agent-session.prompt(), which throws formatNoModelSelectedMessage() before
// streaming. Without the guard this panics with a nil pointer dereference.
func TestAgent_Send_NilModel_ReturnsErrorNotPanic(t *testing.T) {
	a := NewAgent(AgentOptions{Model: nil})
	msgs, err := a.Send(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected an error when no model is selected, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "no model") {
		t.Fatalf("expected a 'no model' error, got: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected no messages appended on a rejected send, got %d", len(msgs))
	}
}

// A non-nil model with a nil Provider is the same hazard on the Provider.Stream
// path; guard it too so a half-built model cannot panic the loop.
func TestAgent_Send_NilProvider_ReturnsErrorNotPanic(t *testing.T) {
	a := NewAgent(AgentOptions{Model: &ai.Model{ID: "x", Provider: nil}})
	if _, err := a.Send(context.Background(), "hello"); err == nil {
		t.Fatal("expected an error when the model has no provider, got nil")
	}
}

// ─── Fake helpers ─────────────────────────────────────────────────────────────

func agentTestStream(events []ai.AssistantMessageEvent) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	for _, event := range events {
		if err := stream.Push(event); err != nil {
			panic(err)
		}
	}
	return stream
}

func agentTestAssistant(content []ai.AssistantContentBlock, reason ai.StopReason) *ai.AssistantMessage {
	return &ai.AssistantMessage{Content: content, Provider: "test", Model: "fake", StopReason: reason}
}

// staticProvider replays a fixed event sequence on every Stream call.
type staticProvider struct {
	events []ai.AssistantMessageEvent
}

func (p *staticProvider) ID() string   { return "static-fake" }
func (p *staticProvider) Close() error { return nil }
func (p *staticProvider) Stream(_ context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	return agentTestStream(p.events), nil
}

// sequencedProvider returns a different event sequence on each successive call.
// When all sequences are exhausted it returns an empty stream.
type sequencedProvider struct {
	seqs [][]ai.AssistantMessageEvent
	idx  atomic.Int32
}

func (p *sequencedProvider) ID() string   { return "seq-fake" }
func (p *sequencedProvider) Close() error { return nil }
func (p *sequencedProvider) Stream(_ context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	i := int(p.idx.Add(1)) - 1
	var events []ai.AssistantMessageEvent
	if i < len(p.seqs) {
		events = p.seqs[i]
	} else {
		events = textSeq("")
	}
	return agentTestStream(events), nil
}

type recordingProvider struct {
	id   string
	seqs [][]ai.AssistantMessageEvent
	idx  atomic.Int32
	mu   sync.Mutex
	opts []ai.StreamOptions
	err  error
}

func (p *recordingProvider) ID() string {
	if p.id != "" {
		return p.id
	}
	return "recording-fake"
}
func (p *recordingProvider) Close() error { return nil }
func (p *recordingProvider) Stream(_ context.Context, _ ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.opts = append(p.opts, opts)
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	i := int(p.idx.Add(1)) - 1
	var events []ai.AssistantMessageEvent
	if i < len(p.seqs) {
		events = p.seqs[i]
	} else {
		events = textSeq("")
	}
	return agentTestStream(events), nil
}

func (p *recordingProvider) StreamOptions() []ai.StreamOptions {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ai.StreamOptions, len(p.opts))
	copy(out, p.opts)
	return out
}

func providerFromSeqs(seqs ...[]ai.AssistantMessageEvent) *sequencedProvider {
	return &sequencedProvider{seqs: seqs}
}

// toolCallSeq builds a stream sequence that emits tool calls followed by done.
func toolCallSeq(calls ...struct{ id, name string }) []ai.AssistantMessageEvent {
	content := make([]ai.AssistantContentBlock, 0, len(calls))
	start := agentTestAssistant(nil, ai.StopReasonPending)
	events := []ai.AssistantMessageEvent{ai.StartEvent{Partial: start}}
	for _, call := range calls {
		index := len(content)
		toolCall := ai.ToolCall{ID: call.id, Name: call.name, Arguments: ai.JsonObject{}}
		content = append(content, toolCall)
		partial := agentTestAssistant(append([]ai.AssistantContentBlock(nil), content...), ai.StopReasonPending)
		events = append(events,
			ai.ToolCallStartEvent{ContentIndex: index, Partial: partial},
			ai.ToolCallDeltaEvent{ContentIndex: index, Delta: `{}`, Partial: partial},
			ai.ToolCallEndEvent{ContentIndex: index, ToolCall: toolCall, Partial: partial},
		)
	}
	final := agentTestAssistant(content, ai.StopReasonToolUse)
	return append(events, ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: final})
}

// textSeq builds a stream sequence that emits text + done (no tools).
func textSeq(text string) []ai.AssistantMessageEvent {
	start := agentTestAssistant(nil, ai.StopReasonPending)
	content := []ai.AssistantContentBlock(nil)
	events := []ai.AssistantMessageEvent{ai.StartEvent{Partial: start}}
	if text != "" {
		content = []ai.AssistantContentBlock{ai.TextContent{Text: text}}
		partial := agentTestAssistant(content, ai.StopReasonPending)
		events = append(events,
			ai.TextStartEvent{ContentIndex: 0, Partial: partial},
			ai.TextDeltaEvent{ContentIndex: 0, Delta: text, Partial: partial},
			ai.TextEndEvent{ContentIndex: 0, Content: text, Partial: partial},
		)
	}
	final := agentTestAssistant(content, ai.StopReasonStop)
	return append(events, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final})
}

func fakeTestModel(prov ai.Provider) *ai.Model {
	return &ai.Model{
		ID:           "fake",
		DisplayName:  "fake",
		Provider:     prov,
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
}

// fakeTool is an AgentTool for unit tests.
type fakeTool struct {
	name    string
	mode    ToolExecutionMode
	delay   time.Duration
	content string
	isError bool
	execErr error          // non-nil = Execute returns a Go error (tool threw)
	params  map[string]any // non-nil = JSON schema enforced before Execute
}

func (t *fakeTool) Name() string                     { return t.name }
func (t *fakeTool) Label() string                    { return "" }
func (t *fakeTool) Description() string              { return t.name }
func (t *fakeTool) Schema() ai.ToolSchema            { return ai.ToolSchema{Name: t.name, Parameters: t.params} }
func (t *fakeTool) ExecutionMode() ToolExecutionMode { return t.mode }
func (t *fakeTool) Execute(_ context.Context, _ string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	if t.delay > 0 {
		time.Sleep(t.delay)
	}
	if t.execErr != nil {
		return AgentToolResult{}, t.execErr
	}
	return AgentToolResult{Content: t.content, IsError: t.isError}, nil
}

// ─── AfterToolCall hooks ─────────────────────────────────────────────────

// TestAfterToolCallContentOverride verifies a hook can replace tool result content.
// Upstream ref: agent-loop.ts finalizeExecutedToolCall (lines 605-648).
func TestAfterToolCallContentOverride(t *testing.T) {
	tool := &fakeTool{name: "mytool", mode: ToolModeSequential, content: "original"}

	prov := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"tc-1", "mytool"}),
		textSeq("done"),
	)
	overridden := "overridden-content"

	var hookCalls int
	a := NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		Tools:    []AgentTool{tool},
		MaxTurns: 5,
		AfterToolCall: []AfterToolCallHook{
			func(_ context.Context, _, _ string, _ json.RawMessage, _ AgentToolResult) AfterToolCallResult {
				hookCalls++
				return AfterToolCallResult{Content: &overridden}
			},
		},
	})

	msgs, err := a.Send(context.Background(), "test")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if hookCalls != 1 {
		t.Errorf("AfterToolCall called %d times, want 1", hookCalls)
	}
	var found bool
	for _, m := range msgs {
		if m.ToolResult != nil && m.ToolResult.ToolName == "mytool" {
			found = true
			if m.ToolResult.Text() != "overridden-content" {
				t.Errorf("content = %q, want %q", m.ToolResult.Text(), "overridden-content")
			}
		}
	}
	if !found {
		t.Error("no mytool result in messages")
	}
}

func TestAfterToolCallImagesOverride(t *testing.T) {
	tool := &fakeTool{name: "image-tool", mode: ToolModeSequential, content: "original"}
	provider := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"tc-image", "image-tool"}),
		textSeq("done"),
	)
	images := []ai.ImageContent{{MimeType: "image/png", Data: "normalized"}}
	agent := NewAgent(AgentOptions{
		Model:    fakeTestModel(provider),
		Tools:    []AgentTool{tool},
		MaxTurns: 5,
		AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
			return AfterToolCallResult{Images: &images}
		}},
	})
	messages, err := agent.Send(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range messages {
		if message.ToolResult == nil {
			continue
		}
		got := message.ToolResult.Images()
		if len(got) != 1 || got[0].Data != "normalized" {
			t.Fatalf("tool result images = %+v", got)
		}
		return
	}
	t.Fatal("tool result message missing")
}

// TestAfterToolCallTerminate verifies Terminate=true stops the loop after one batch.
// Upstream ref: agent-loop.ts shouldTerminateToolBatch (lines 499-501).
func TestAfterToolCallTerminate(t *testing.T) {
	tool := &fakeTool{name: "stoptool", mode: ToolModeSequential, content: "result"}

	prov := providerFromSeqs(
		toolCallSeq(struct{ id, name string }{"tc-stop", "stoptool"}),
		textSeq("second turn: should not reach"),
	)

	a := NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		Tools:    []AgentTool{tool},
		MaxTurns: 10, // high: terminate must fire before this
		AfterToolCall: []AfterToolCallHook{
			func(_ context.Context, _, _ string, _ json.RawMessage, _ AgentToolResult) AfterToolCallResult {
				return AfterToolCallResult{Terminate: new(true)}
			},
		},
	})

	msgs, err := a.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var assistantTurns int
	for _, m := range msgs {
		if m.Assistant != nil {
			assistantTurns++
		}
	}
	// Exactly 1 assistant turn: the tool-call turn. The second LLM call
	// must NOT have happened because terminate=true broke the loop.
	if assistantTurns != 1 {
		t.Errorf("expected 1 assistant turn (terminate), got %d", assistantTurns)
	}
}

// ─── Parallel tool execution ────────────────────────────────────────────

// TestParallelToolBatch: wall-clock must be < sum of serial sleeps.
// Upstream ref: agent-loop.ts executeToolCallsParallel (lines 412-469).
func TestParallelToolBatch(t *testing.T) {
	const sleep = 60 * time.Millisecond
	names := []struct{ id, name string }{
		{"tc-0", "ptool0"},
		{"tc-1", "ptool1"},
		{"tc-2", "ptool2"},
	}
	tools := make([]AgentTool, len(names))
	for i, n := range names {
		tools[i] = &fakeTool{name: n.name, mode: ToolModeParallel, delay: sleep, content: "ok"}
	}

	prov := providerFromSeqs(toolCallSeq(names...), textSeq("done"))
	a := NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		Tools:    tools,
		MaxTurns: 5,
	})

	start := time.Now()
	_, err := a.Send(context.Background(), "parallel")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	serial := sleep * time.Duration(len(names))
	if elapsed >= serial {
		t.Errorf("elapsed %v >= serial %v: tools did not run concurrently", elapsed, serial)
	}
	t.Logf("%d tools x %v: elapsed=%v (serial=%v): concurrent OK", len(names), sleep, elapsed, serial)
}

// TestParallelResultsInSourceOrder: results must be in source order even if tools
// complete out of order. Upstream contract: agent-loop.ts:455-468 (Promise.all
// preserves input order).
func TestParallelResultsInSourceOrder(t *testing.T) {
	// slow finishes later, fast finishes first: results must still be [slow, fast].
	names := []struct{ id, name string }{{"tc-s", "slow"}, {"tc-f", "fast"}}
	tools := []AgentTool{
		&fakeTool{name: "slow", mode: ToolModeParallel, delay: 80 * time.Millisecond, content: "slow-result"},
		&fakeTool{name: "fast", mode: ToolModeParallel, delay: 0, content: "fast-result"},
	}
	prov := providerFromSeqs(toolCallSeq(names...), textSeq("done"))
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: tools, MaxTurns: 5})

	msgs, err := a.Send(context.Background(), "order")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	var results []string
	for _, m := range msgs {
		if m.ToolResult != nil {
			results = append(results, m.ToolResult.Text())
		}
	}
	if len(results) != 2 {
		t.Fatalf("want 2 results, got %d: %v", len(results), results)
	}
	if results[0] != "slow-result" || results[1] != "fast-result" {
		t.Errorf("out of order: %v", results)
	}
}

// TestMixedModeFallsBackToSequential: one sequential tool → whole batch sequential.
// Upstream ref: agent-loop.ts:348-351 hasSequentialToolCall check.
func TestMixedModeFallsBackToSequential(t *testing.T) {
	const sleep = 60 * time.Millisecond
	names := []struct{ id, name string }{{"tc-p", "par"}, {"tc-s", "seq"}}
	tools := []AgentTool{
		&fakeTool{name: "par", mode: ToolModeParallel, delay: sleep, content: "par"},
		&fakeTool{name: "seq", mode: ToolModeSequential, delay: sleep, content: "seq"},
	}
	prov := providerFromSeqs(toolCallSeq(names...), textSeq("done"))
	a := NewAgent(AgentOptions{Model: fakeTestModel(prov), Tools: tools, MaxTurns: 5})

	start := time.Now()
	_, err := a.Send(context.Background(), "mixed")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Sequential: elapsed ≥ 2×sleep (both ran serially).
	serial := sleep * 2
	if elapsed < serial-20*time.Millisecond {
		t.Errorf("mixed batch elapsed %v < serial bound %v: should have been sequential", elapsed, serial)
	}
	t.Logf("mixed-mode sequential: elapsed=%v >= ~serial=%v OK", elapsed, serial)
}

// TestOnMessagePersistFiresPerProducedMessage proves the per-message persistence
// hook fires incrementally as each message is produced: the user prompt (via
// the runLoop message_end replay), assistant (toolUse), tool result, assistant
// (text): driven by message_end in emit(), mirroring upstream's single
// persistence site (agent-session.ts:511-525). This guards the incremental-
// persistence fix: batching persistence at turn end lost an entire in-flight
// turn when the process was killed mid-turn and resumed.
func TestOnMessagePersistFiresPerProducedMessage(t *testing.T) {
	tools := []AgentTool{&fakeTool{name: "echo", mode: ToolModeParallel, content: "echoed"}}
	prov := providerFromSeqs(toolCallSeq(struct{ id, name string }{"tc1", "echo"}), textSeq("done"))

	var persisted []string
	a := NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		Tools:    tools,
		MaxTurns: 5,
		OnMessagePersist: func(m AgentMessage) error {
			switch {
			case m.User != nil:
				persisted = append(persisted, "user")
			case m.Assistant != nil:
				persisted = append(persisted, "assistant")
			case m.ToolResult != nil:
				persisted = append(persisted, "tool")
			}
			return nil
		},
	})

	if _, err := a.Send(context.Background(), "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Every message is persisted once via message_end, including the user prompt
	// (upstream persists user/assistant/toolResult uniformly). Production order:
	// user→assistant→tool→assistant.
	want := []string{"user", "assistant", "tool", "assistant"}
	if len(persisted) != len(want) {
		t.Fatalf("hook fired %d times %v, want %d %v", len(persisted), persisted, len(want), want)
	}
	for i := range want {
		if persisted[i] != want[i] {
			t.Fatalf("hook order[%d]=%q, want %q (full: %v)", i, persisted[i], want[i], persisted)
		}
	}
}

// TestOnMessagePersistSkipsResumedHistory proves the message_end-driven persist
// does NOT re-persist loaded/resumed context. On resume, history is loaded via
// SetMessages (already on disk); only THIS run's new messages (the prompt and
// the new assistant) must fire the hook. The runLoop replay covers
// a.messages[runStart:], never the loaded prefix, so resuming a long session
// does not duplicate every prior entry. Mirrors upstream, where loaded entries
// never re-emit message_end.
func TestOnMessagePersistSkipsResumedHistory(t *testing.T) {
	prov := providerFromSeqs(textSeq("reply"))
	var persisted []string
	a := NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		MaxTurns: 5,
		OnMessagePersist: func(m AgentMessage) error {
			persisted = append(persisted, m.Role())
			return nil
		},
	})

	// Simulate resume: two already-persisted messages loaded into context.
	a.SetMessages([]AgentMessage{
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "old prompt"}}}},
		{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "old reply"}}, StopReason: "stop"}},
	})

	if _, err := a.Send(context.Background(), "new prompt"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Only the new prompt + new assistant persist; the 2 loaded entries do not.
	want := []string{"user", "assistant"}
	if len(persisted) != len(want) {
		t.Fatalf("hook fired %d times %v, want %d %v (resumed history must not re-persist)",
			len(persisted), persisted, len(want), want)
	}
	for i := range want {
		if persisted[i] != want[i] {
			t.Fatalf("hook order[%d]=%q, want %q (full: %v)", i, persisted[i], want[i], persisted)
		}
	}
}

// ─── 4.x: stopReason plumbing ─────────────────────────────────────────────────

// TestStopReasonStop verifies that a normal text-only stream produces
// StopReason="stop" on the assistant message.
func TestStopReasonStop(t *testing.T) {
	evs := textSeq("hello")
	a := &Agent{}
	msg, calls, err := a.consumeStream(context.Background(), agentTestStream(evs), a.Model())
	if err != nil {
		t.Fatalf("consumeStream error: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(calls))
	}
	if msg.StopReason != "stop" {
		t.Errorf("expected StopReason=stop, got %q", msg.StopReason)
	}
}

// TestStopReasonToolUse verifies that a stream with tool calls produces
// StopReason="toolUse" on the assistant message.
func TestStopReasonToolUse(t *testing.T) {
	evs := toolCallSeq(struct{ id, name string }{"call_1", "bash"})
	a := &Agent{}
	msg, calls, err := a.consumeStream(context.Background(), agentTestStream(evs), a.Model())
	if err != nil {
		t.Fatalf("consumeStream error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if msg.StopReason != "toolUse" {
		t.Errorf("expected StopReason=toolUse, got %q", msg.StopReason)
	}
}

// TestStopReasonAborted verifies that context cancellation produces
// StopReason="aborted" on the partial assistant message.
// Strategy: pre-cancel the context, send a partial stream (EventStart only),
// close the channel. consumeStream's post-loop ctx.Err() check fires and sets
// StopReason="aborted" before returning.
func TestStopReasonAborted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so ctx.Err() is already set

	partial := agentTestAssistant(nil, ai.StopReasonPending)
	stream := ai.NewAssistantMessageEventStream()
	if err := stream.Push(ai.StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}

	a := &Agent{}
	msg, _, err := a.consumeStream(ctx, stream, nil)
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if msg.StopReason != "aborted" {
		t.Errorf("expected StopReason=aborted, got %q", msg.StopReason)
	}
}

// TestFinishTurnEndStopsAfterTurn: FinishTurn's "end" action stops the run
// before the queues are polled, leaving the follow-up queued.
func TestFinishTurnEndStopsAfterTurn(t *testing.T) {
	prov := providerFromSeqs(
		textSeq("first turn"),
		textSeq("second turn should not run"),
	)
	events := make(chan AgentEvent, 16)
	var stopCalls int
	var agent *Agent

	agent = NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		MaxTurns: 5,
		EventCh:  events,
		FinishTurn: func(_ context.Context, ctx AgentTurnContext) *AgentTurnDecision {
			stopCalls++
			if ctx.Message == nil {
				t.Fatal("finishTurn got no assistant message")
			}
			if got := len(ctx.ToolResults); got != 0 {
				t.Fatalf("toolResults len = %d, want 0", got)
			}
			if got := len(ctx.NewMessages); got != 2 {
				t.Fatalf("newMessages len = %d, want 2", got)
			}
			if got := len(ctx.Context); got != len(agent.Messages()) {
				t.Fatalf("context len = %d, want %d", got, len(agent.Messages()))
			}
			return &AgentTurnDecision{Action: AgentTurnEnd}
		},
	})
	agent.FollowUp(AgentMessage{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "queued follow-up"}}, Timestamp: time.Now().UnixMilli()}})

	msgs, err := agent.Send(context.Background(), "start")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if stopCalls != 1 {
		t.Fatalf("finishTurn called %d times, want 1", stopCalls)
	}
	if prov.idx.Load() != 1 {
		t.Fatalf("provider Stream calls = %d, want 1", prov.idx.Load())
	}
	if agent.followUpQueue.Len() != 1 {
		t.Fatalf("follow-up queue len = %d, want 1", agent.followUpQueue.Len())
	}

	var assistantTurns int
	for _, m := range msgs {
		if m.Assistant != nil {
			assistantTurns++
		}
	}
	if assistantTurns != 1 {
		t.Fatalf("assistant turns = %d, want 1", assistantTurns)
	}

	var sawTurnEnd, sawAgentEnd bool
	for len(events) > 0 {
		switch (<-events).(type) {
		case TurnEndEvent:
			sawTurnEnd = true
		case AgentEndEvent:
			sawAgentEnd = true
		}
	}
	if !sawTurnEnd {
		t.Fatal("missing TurnEndEvent")
	}
	if !sawAgentEnd {
		t.Fatal("missing AgentEndEvent")
	}
}

func TestPrepareNextTurnUpdatesModelAndThinking(t *testing.T) {
	firstProv := &recordingProvider{seqs: [][]ai.AssistantMessageEvent{textSeq("first")}}
	secondProv := &recordingProvider{id: "replacement-provider", seqs: [][]ai.AssistantMessageEvent{textSeq("second")}}
	firstModel := fakeTestModel(firstProv)
	secondModel := &ai.Model{
		ID:           "fake-2",
		DisplayName:  "fake-2",
		Provider:     secondProv,
		Capabilities: ai.ModelCapabilities{ContextWindow: 8000},
	}
	minimal := ai.ThinkingMinimal
	var prepareCalls int

	a := NewAgent(AgentOptions{
		Model:    firstModel,
		MaxTurns: 5,
		PrepareNextTurn: func(_ context.Context, ctx PrepareNextTurnContext) *AgentLoopTurnUpdate {
			prepareCalls++
			if ctx.Message == nil {
				t.Fatal("prepareNextTurn got no assistant message")
			}
			if prepareCalls == 1 {
				return &AgentLoopTurnUpdate{Model: secondModel, ThinkingLevel: &minimal}
			}
			return nil
		},
		FinishTurn: func(_ context.Context, ctx AgentTurnContext) *AgentTurnDecision {
			if len(ctx.NewMessages) >= 4 {
				return &AgentTurnDecision{Action: AgentTurnEnd}
			}
			return nil
		},
	})
	a.FollowUp(AgentMessage{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "queued follow-up"}}, Timestamp: time.Now().UnixMilli()}})

	msgs, err := a.Send(context.Background(), "start")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Upstream prepares only a turn that follows: the follow-up turn.
	if prepareCalls != 1 {
		t.Fatalf("prepareNextTurn calls = %d, want 1", prepareCalls)
	}
	if got := firstProv.idx.Load(); got != 1 {
		t.Fatalf("first provider calls = %d, want 1", got)
	}
	if got := secondProv.idx.Load(); got != 1 {
		t.Fatalf("second provider calls = %d, want 1", got)
	}
	secondOpts := secondProv.StreamOptions()
	if len(secondOpts) != 1 {
		t.Fatalf("second provider stream options = %d, want 1", len(secondOpts))
	}
	if secondOpts[0].Thinking != ai.ThinkingMinimal {
		t.Fatalf("second provider thinking = %q, want %q", secondOpts[0].Thinking, ai.ThinkingMinimal)
	}
	if a.Model() != firstModel {
		t.Fatal("PrepareNextTurn should not permanently replace agent default model")
	}
	var assistantTurns int
	for _, m := range msgs {
		if m.Assistant != nil {
			assistantTurns++
		}
	}
	if assistantTurns != 2 {
		t.Fatalf("assistant turns = %d, want 2", assistantTurns)
	}
}

func TestProviderStreamErrorEmitsFailureLifecycle(t *testing.T) {
	prov := &recordingProvider{err: errors.New("boom")}
	events := make(chan AgentEvent, 16)
	a := NewAgent(AgentOptions{
		Model:    fakeTestModel(prov),
		MaxTurns: 5,
		EventCh:  events,
	})

	msgs, err := a.Send(context.Background(), "start")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(msgs) < 2 {
		t.Fatalf("messages len = %d, want at least 2", len(msgs))
	}
	last := msgs[len(msgs)-1]
	if last.Assistant == nil {
		t.Fatal("last message is not assistant")
	}
	if last.Assistant.StopReason != "error" {
		t.Fatalf("stopReason = %q, want error", last.Assistant.StopReason)
	}
	if last.Assistant.ErrorMessage != "boom" {
		t.Fatalf("errorMessage = %q, want boom", last.Assistant.ErrorMessage)
	}
	if len(last.Assistant.Content) != 1 {
		t.Fatalf("error assistant content len = %d, want 1", len(last.Assistant.Content))
	}
	if txt, ok := last.Assistant.Content[0].(ai.TextContent); !ok || txt.Text != "" {
		t.Fatalf("error assistant content = %#v, want empty text block", last.Assistant.Content[0])
	}

	var seq []string
	for len(events) > 0 {
		switch (<-events).(type) {
		case MessageStartEvent:
			seq = append(seq, "message_start")
		case MessageEndEvent:
			seq = append(seq, "message_end")
		case TurnEndEvent:
			seq = append(seq, "turn_end")
		case AgentEndEvent:
			seq = append(seq, "agent_end")
		}
	}
	want := []string{"message_start", "message_end", "turn_end", "agent_end"}
	if len(seq) < len(want) {
		t.Fatalf("event sequence too short: got %v want suffix %v", seq, want)
	}
	got := seq[len(seq)-len(want):]
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("event order = %v, want suffix %v", got, want)
		}
	}
}

// ─── Encrypted Reasoning Flow Test ───────────────────────────────────────────
// Verifies that thought signatures flow through the full agent loop:
// SSE stream → consumeStream → AssistantMessage.Content → ToolCall.ThoughtSignature

func TestAgent_ThoughtSignatureFlow(t *testing.T) {
	// Simulate a stream that:
	// 1. Starts a tool call (id="call_test")
	// 2. Sends tool call name and args
	// 3. Sends a reasoning_details thought signature for that tool call
	// 4. Sends done
	call := ai.ToolCall{
		ID: "call_test", Name: "read", Arguments: ai.JsonObject{"path": "main.go"},
		ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_test","data":"encrypted_blob"}`,
	}
	partial := agentTestAssistant([]ai.AssistantContentBlock{call}, ai.StopReasonPending)
	final := agentTestAssistant([]ai.AssistantContentBlock{call}, ai.StopReasonToolUse)
	final.Usage = ai.Usage{Input: 10, Output: 5}
	provider := &staticProvider{events: []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)},
		ai.ToolCallStartEvent{ContentIndex: 0, Partial: partial},
		ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: `{"path":"main.go"}`, Partial: partial},
		ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: final},
	}}

	// Create a tool that just returns success
	dummyTool := &fakeToolForSignatureTest{}
	agent := NewAgent(AgentOptions{
		Model: &ai.Model{
			ID:       "test-model",
			Provider: provider,
		},
		Tools:    []AgentTool{dummyTool},
		MaxTurns: 1,
	})

	// The static provider always answers with a tool call; the cap stops it.
	msgs, err := agent.Send(context.Background(), "test prompt")
	if err != nil && !errors.Is(err, ErrMaxTurnsReached) {
		t.Fatal(err)
	}

	// Find the assistant message with tool calls
	var assistantMsg *AssistantMessage
	for _, m := range msgs {
		if m.Assistant != nil && len(m.Assistant.Content) > 0 {
			assistantMsg = m.Assistant
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("no assistant message found")
		return
	}

	// Verify the ToolCall has the ThoughtSignature
	var foundToolUse *ai.ToolCall
	for _, block := range assistantMsg.Content {
		if tu, ok := block.(ai.ToolCall); ok && tu.ID == "call_test" {
			foundToolUse = &tu
			break
		}
	}
	if foundToolUse == nil {
		t.Fatal("no ToolCall with id='call_test' found in assistant message")
		return
	}
	if foundToolUse.ThoughtSignature == "" {
		t.Fatal("ThoughtSignature is empty: not propagated from stream")
	}

	expectedSig := `{"type":"reasoning.encrypted","id":"call_test","data":"encrypted_blob"}`
	if foundToolUse.ThoughtSignature != expectedSig {
		t.Errorf("ThoughtSignature mismatch:\n  got:  %s\n  want: %s",
			foundToolUse.ThoughtSignature, expectedSig)
	}

	// Verify the tool was actually called (proves the agent loop ran)
	if !dummyTool.called.Load() {
		t.Error("tool was never called: agent loop did not execute tool")
	}
}

// fakeToolForSignatureTest is a minimal tool for the thought signature test.
type fakeToolForSignatureTest struct {
	called atomic.Bool
}

func (f *fakeToolForSignatureTest) Name() string  { return "read" }
func (f *fakeToolForSignatureTest) Label() string { return "" }
func (f *fakeToolForSignatureTest) Schema() ai.ToolSchema {
	return ai.ToolSchema{
		Name:        "read",
		Description: "Read a file",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		},
	}
}
func (f *fakeToolForSignatureTest) Execute(_ context.Context, _ string, _ json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	f.called.Store(true)
	return AgentToolResult{Content: "file contents"}, nil
}

func (f *fakeToolForSignatureTest) ExecutionMode() ToolExecutionMode { return ToolModeParallel }

// abortingBatch runs a two-call batch whose first before hook cancels the
// run, and returns the tool results and the started call IDs.
func abortingBatch(t *testing.T, mode ToolExecutionMode) ([]ToolResultMessage, []string, int32) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var beforeCalls atomic.Int32
	var starts []string
	rec := newEventRecorder(func(ev AgentEvent) {
		if start, ok := ev.(ToolExecutionStartEvent); ok {
			starts = append(starts, start.ToolCallID)
		}
	})
	a := NewAgent(AgentOptions{
		Model: scriptedModel(&scriptedProvider{respond: toolCallsThenText(toolCall("call-1", "a", nil), toolCall("call-2", "b", nil))}),
		Tools: []AgentTool{
			&scriptTool{name: "a", mode: mode, params: map[string]any{"type": "object"}},
			&scriptTool{name: "b", mode: mode, params: map[string]any{"type": "object"}},
		},
		EventCh: rec.ch,
		BeforeToolCall: []BeforeToolCallHook{
			func(_ context.Context, _, _ string, _ json.RawMessage) ToolCallHookResult {
				if beforeCalls.Add(1) == 1 {
					cancel()
				}
				return ToolCallHookResult{}
			},
		},
	})
	msgs, _ := a.Send(ctx, "run")
	rec.stop()
	var results []ToolResultMessage
	for _, m := range msgs {
		if m.ToolResult != nil {
			results = append(results, *m.ToolResult)
		}
	}
	return results, starts, beforeCalls.Load()
}

func checkAbortedBatch(t *testing.T, results []ToolResultMessage, starts []string, beforeCalls int32) {
	t.Helper()
	if len(results) != 1 || results[0].Text() != "Operation aborted" || !results[0].IsError {
		t.Fatalf("results = %+v, want one aborted error result", results)
	}
	if !reflect.DeepEqual(starts, []string{"call-1"}) || beforeCalls != 1 {
		t.Fatalf("started %v, before hook calls %d; want only call-1", starts, beforeCalls)
	}
}

func TestExecuteSequentialBreaksAfterContextAbort(t *testing.T) {
	results, starts, beforeCalls := abortingBatch(t, ToolModeSequential)
	checkAbortedBatch(t, results, starts, beforeCalls)
}

// Upstream executeToolCallsParallel stops preparing calls once the run is
// aborted, so later calls never start.
func TestExecuteParallelSkipsUnstartedCallsAfterContextAbort(t *testing.T) {
	results, starts, beforeCalls := abortingBatch(t, ToolModeParallel)
	checkAbortedBatch(t, results, starts, beforeCalls)
}

func TestNormalizeMessagesDropsErroredAndAbortedAssistantTurns(t *testing.T) {
	msgs := []AgentMessage{
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "hello"}}}},
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "error", ErrorMessage: "boom", Content: []ai.AssistantContentBlock{ai.TextContent{Text: ""}}}},
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "aborted", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "partial"}}}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "next"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	if len(got) != 2 {
		t.Fatalf("len(NormalizeMessages) = %d, want 2: %#v", len(got), got)
	}
	if got[0].User == nil || got[1].User == nil {
		t.Fatalf("expected only user messages after dropping incomplete assistant turns: %#v", got)
	}
}

func TestNormalizeMessagesSynthesizesMissingToolResults(t *testing.T) {
	msgs := []AgentMessage{
		{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: "toolUse", Content: []ai.AssistantContentBlock{
			ai.ToolCall{ID: "call_1", Name: "read", Arguments: ai.JsonObject{"path": "x.go"}},
		}}},
		{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: "interrupt"}}}},
	}
	got := NormalizeMessages(msgs, nil)
	if len(got) != 3 {
		t.Fatalf("len(NormalizeMessages) = %d, want assistant + synthetic tool result + user: %#v", len(got), got)
	}
	if got[1].ToolResult == nil {
		t.Fatalf("middle message should be synthetic tool result: %#v", got[1])
	}
	res := got[1].ToolResult
	if res.ToolCallID != "call_1" || res.ToolName != "read" || res.Text() != "No result provided" || !res.IsError {
		t.Fatalf("synthetic tool result = %#v", res)
	}
}

func TestAgent_ThinkingContentInContent(t *testing.T) {
	// Verify that thinking + text + tool calls produce the correct Content
	// array: [ThinkingContent, TextContent, ToolCall...].
	// This is the regression test for the bug where Anthropic tool calls
	// at API indexes 2,3 (after thinking=0, text=1) were lost because
	// the agent core expected 0-based sequential tool map keys.
	content := []ai.AssistantContentBlock{
		ai.ThinkingContent{Thinking: "Let me think about this", ThinkingSignature: "sig_opaque"},
		ai.TextContent{Text: "I'll read the files."},
		ai.ToolCall{ID: "call_a", Name: "read", Arguments: ai.JsonObject{"path": "a.go"}},
		ai.ToolCall{ID: "call_b", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}},
	}
	final := agentTestAssistant(content, ai.StopReasonToolUse)
	final.Usage = ai.Usage{Input: 100, Output: 50}
	provider := &staticProvider{events: []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)},
		ai.DoneEvent{Reason: ai.StopReasonToolUse, Message: final},
	}}
	readTool := &fakeTool{name: "read"}
	bashTool := &fakeTool{name: "bash"}
	agent := NewAgent(AgentOptions{
		Model: &ai.Model{
			ID:       "test-model",
			Provider: provider,
		},
		Tools:    []AgentTool{readTool, bashTool},
		MaxTurns: 1,
	})

	// The static provider always answers with tool calls; the cap stops it.
	msgs, err := agent.Send(context.Background(), "test")
	if err != nil && !errors.Is(err, ErrMaxTurnsReached) {
		t.Fatal(err)
	}

	// Find the assistant message.
	var asst *AssistantMessage
	for _, m := range msgs {
		if m.Assistant != nil {
			asst = m.Assistant
			break
		}
	}
	if asst == nil {
		t.Fatal("no assistant message found")
	}

	// Content should have: ThinkingContent, TextContent, 2x ToolCall
	if len(asst.Content) != 4 {
		types := make([]string, len(asst.Content))
		for i, b := range asst.Content {
			types[i] = reflect.TypeOf(b).String()
		}
		t.Fatalf("Content len=%d types=%v, want 4 [Thinking, Text, Tool, Tool]", len(asst.Content), types)
	}

	tc, ok := asst.Content[0].(ai.ThinkingContent)
	if !ok {
		t.Fatalf("[0] = %T, want ThinkingContent", asst.Content[0])
	}
	if tc.Thinking != "Let me think about this" {
		t.Errorf("thinking = %q", tc.Thinking)
	}
	if tc.ThinkingSignature != "sig_opaque" {
		t.Errorf("thinkingSignature = %q", tc.ThinkingSignature)
	}

	txt, ok := asst.Content[1].(ai.TextContent)
	if !ok {
		t.Fatalf("[1] = %T, want TextContent", asst.Content[1])
	}
	if txt.Text != "I'll read the files." {
		t.Errorf("text = %q", txt.Text)
	}

	tool0, ok := asst.Content[2].(ai.ToolCall)
	if !ok {
		t.Fatalf("[2] = %T, want ToolCall", asst.Content[2])
	}
	if tool0.Name != "read" || tool0.ID != "call_a" {
		t.Errorf("tool0 = %s/%s", tool0.Name, tool0.ID)
	}

	tool1, ok := asst.Content[3].(ai.ToolCall)
	if !ok {
		t.Fatalf("[3] = %T, want ToolCall", asst.Content[3])
	}
	if tool1.Name != "bash" || tool1.ID != "call_b" {
		t.Errorf("tool1 = %s/%s", tool1.Name, tool1.ID)
	}

	// Flat fields should also be set (backward compat).
	if asst.Thinking != "Let me think about this" {
		t.Errorf("flat Thinking = %q", asst.Thinking)
	}
	if asst.ThinkingSignature != "sig_opaque" {
		t.Errorf("flat ThinkingSignature = %q", asst.ThinkingSignature)
	}
}
