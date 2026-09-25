package agent

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Helpers for the ports of upstream packages/agent/test/agent-loop.test.ts and
// agent.test.ts. They stand in for upstream's MockAssistantStream, stream
// functions, and subscribe() listeners.

// scriptedRequest is one provider request the agent made.
type scriptedRequest struct {
	ctx        context.Context
	transcript ai.TranscriptContext
	opts       ai.StreamOptions
}

// scriptedProvider answers each request with the stream built by respond.
// call is 1-based, like upstream tests' post-increment counters.
type scriptedProvider struct {
	id       string
	respond  func(call int, req scriptedRequest) *ai.AssistantMessageEventStream
	mu       sync.Mutex
	requests []scriptedRequest
}

func (p *scriptedProvider) ID() string {
	if p.id != "" {
		return p.id
	}
	return "scripted"
}

func (p *scriptedProvider) Close() error { return nil }

func (p *scriptedProvider) Stream(ctx context.Context, transcript ai.TranscriptContext, opts ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	req := scriptedRequest{ctx: ctx, transcript: transcript, opts: opts}
	p.mu.Lock()
	p.requests = append(p.requests, req)
	call := len(p.requests)
	p.mu.Unlock()
	return p.respond(call, req), nil
}

func (p *scriptedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func (p *scriptedProvider) request(call int) scriptedRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.requests[call-1]
}

func scriptedModel(p *scriptedProvider) *ai.Model {
	return &ai.Model{ID: "mock", DisplayName: "mock", Provider: p, Capabilities: ai.ModelCapabilities{ContextWindow: 8192}}
}

// replyText answers every request with a text response.
func replyText(text string) func(int, scriptedRequest) *ai.AssistantMessageEventStream {
	return func(int, scriptedRequest) *ai.AssistantMessageEventStream { return doneStream(textMessage(text)) }
}

// toolCallsThenText answers the first request with tool calls and every later
// one with a text response.
func toolCallsThenText(calls ...ai.ToolCall) func(int, scriptedRequest) *ai.AssistantMessageEventStream {
	return func(call int, _ scriptedRequest) *ai.AssistantMessageEventStream {
		if call == 1 {
			return doneStream(toolUseMessage(calls...))
		}
		return doneStream(textMessage("done"))
	}
}

func textMessage(text string) *ai.AssistantMessage {
	return agentTestAssistant([]ai.AssistantContentBlock{ai.TextContent{Text: text}}, ai.StopReasonStop)
}

func toolUseMessage(calls ...ai.ToolCall) *ai.AssistantMessage {
	content := make([]ai.AssistantContentBlock, len(calls))
	for i, call := range calls {
		content[i] = call
	}
	return agentTestAssistant(content, ai.StopReasonToolUse)
}

func toolCall(id, name string, args ai.JsonObject) ai.ToolCall {
	if args == nil {
		args = ai.JsonObject{}
	}
	return ai.ToolCall{ID: id, Name: name, Arguments: args}
}

// doneStream is upstream's `stream.push({ type: "done", reason, message })`.
func doneStream(message *ai.AssistantMessage) *ai.AssistantMessageEventStream {
	return agentTestStream([]ai.AssistantMessageEvent{
		ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)},
		ai.DoneEvent{Reason: message.StopReason, Message: message},
	})
}

// errorStream is upstream's `stream.push({ type: "error", reason, error })`.
func errorStream(reason ai.StopReason) *ai.AssistantMessageEventStream {
	message := agentTestAssistant(nil, reason)
	message.ErrorMessage = string(reason)
	return agentTestStream([]ai.AssistantMessageEvent{ai.ErrorEvent{Reason: reason, Error: message}})
}

// abortableStream starts a response, reports it started, and ends it as
// aborted once ctx is cancelled, like upstream's checkAbort streams.
func abortableStream(ctx context.Context, started chan<- struct{}) *ai.AssistantMessageEventStream {
	stream := ai.NewAssistantMessageEventStream()
	_ = stream.Push(ai.StartEvent{Partial: agentTestAssistant(nil, ai.StopReasonPending)})
	go func() {
		started <- struct{}{}
		<-ctx.Done()
		_ = stream.Push(ai.ErrorEvent{Reason: ai.StopReasonAborted, Error: agentTestAssistant(nil, ai.StopReasonAborted)})
	}()
	return stream
}

func userMessage(text string) AgentMessage {
	return AgentMessage{User: &UserMessage{Role: RoleUser, Content: []ai.UserContentBlock{ai.TextContent{Text: text}}, Timestamp: time.Now().UnixMilli()}}
}

func assistantText(text string) AgentMessage {
	return AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, StopReason: ai.StopReasonStop}}
}

// userTexts lists the user text a provider request carried, like upstream
// tests' `context.messages.flatMap(... role === "user" ...)`.
func userTexts(transcript ai.TranscriptContext) []string {
	var texts []string
	for _, message := range transcript.Messages() {
		user, ok := message.(ai.UserMessage)
		if !ok {
			continue
		}
		switch content := user.Content.(type) {
		case ai.UserText:
			texts = append(texts, string(content))
		case ai.UserContentBlocks:
			for _, block := range content {
				if text, ok := block.(ai.TextContent); ok {
					texts = append(texts, text.Text)
				}
			}
		}
	}
	return texts
}

func roles(messages []AgentMessage) []string {
	out := make([]string, len(messages))
	for i, message := range messages {
		out[i] = message.Role()
	}
	return out
}

// valueSchema is upstream's Type.Object({ value: Type.String() }).
var valueSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"value": map[string]any{"type": "string"}},
	"required":   []any{"value"},
}

// scriptTool is an AgentTool whose Execute runs a test-supplied function.
type scriptTool struct {
	name    string
	label   string
	mode    ToolExecutionMode
	params  map[string]any
	execute func(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error)
}

func (t *scriptTool) Name() string  { return t.name }
func (t *scriptTool) Label() string { return t.label }
func (t *scriptTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: t.name, Description: t.name + " tool", Parameters: t.params}
}
func (t *scriptTool) ExecutionMode() ToolExecutionMode { return t.mode }
func (t *scriptTool) Execute(ctx context.Context, id string, args json.RawMessage, onUpdate ToolUpdateCallback) (AgentToolResult, error) {
	if t.execute == nil {
		return AgentToolResult{Content: t.name}, nil
	}
	return t.execute(ctx, id, args, onUpdate)
}

// valueEchoTool is upstream's "echo" tool: it records params.value and returns
// "echoed: <value>".
func valueEchoTool(mode ToolExecutionMode, record func(value string)) *scriptTool {
	return &scriptTool{name: "echo", label: "Echo", mode: mode, params: valueSchema,
		execute: func(_ context.Context, _ string, args json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
			value := argValue(args)
			if record != nil {
				record(value)
			}
			return AgentToolResult{Content: "echoed: " + value, Details: map[string]any{"value": value}}, nil
		}}
}

func argValue(args json.RawMessage) string {
	var decoded struct {
		Value string `json:"value"`
	}
	_ = json.Unmarshal(args, &decoded)
	return decoded.Value
}

// eventRecorder is an EventCh consumer standing in for upstream subscribe().
// The channel is unbuffered, so every emit completes only once the recorder
// has received the event.
type eventRecorder struct {
	ch      chan AgentEvent
	done    chan struct{}
	onEvent func(AgentEvent)
	mu      sync.Mutex
	cond    *sync.Cond
	events  []AgentEvent
}

func newEventRecorder(onEvent func(AgentEvent)) *eventRecorder {
	r := &eventRecorder{ch: make(chan AgentEvent), done: make(chan struct{}), onEvent: onEvent}
	r.cond = sync.NewCond(&r.mu)
	go func() {
		defer close(r.done)
		for ev := range r.ch {
			if r.onEvent != nil {
				r.onEvent(ev)
			}
			r.mu.Lock()
			r.events = append(r.events, ev)
			r.cond.Broadcast()
			r.mu.Unlock()
		}
	}()
	return r
}

// stop closes the channel and returns every event received.
func (r *eventRecorder) stop() []AgentEvent {
	close(r.ch)
	<-r.done
	return r.snapshot()
}

func (r *eventRecorder) snapshot() []AgentEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

// waitFor blocks until an event matching match has been recorded, failing
// the test after a timeout.
func (r *eventRecorder) waitFor(t *testing.T, match func(AgentEvent) bool) {
	t.Helper()
	deadline := time.AfterFunc(5*time.Second, func() {
		r.mu.Lock()
		r.cond.Broadcast()
		r.mu.Unlock()
	})
	defer deadline.Stop()
	start := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for !slices.ContainsFunc(r.events, match) {
		if time.Since(start) >= 5*time.Second {
			t.Error("timed out waiting for an agent event")
			return
		}
		r.cond.Wait()
	}
}

// eventTypes names events the way upstream's event.type does, skipping
// PiG's TimingEvent diagnostics, which upstream does not emit.
func eventTypes(events []AgentEvent) []string {
	var out []string
	for _, ev := range events {
		if name := eventType(ev); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func eventType(ev AgentEvent) string {
	switch ev.(type) {
	case AgentStartEvent:
		return "agent_start"
	case AgentEndEvent:
		return "agent_end"
	case TurnStartEvent:
		return "turn_start"
	case TurnEndEvent:
		return "turn_end"
	case MessageStartEvent:
		return "message_start"
	case MessageUpdateEvent:
		return "message_update"
	case MessageEndEvent:
		return "message_end"
	case ToolExecutionStartEvent:
		return "tool_execution_start"
	case ToolExecutionUpdateEvent:
		return "tool_execution_update"
	case ToolExecutionEndEvent:
		return "tool_execution_end"
	}
	return ""
}

// orderLog is a mutex-guarded ordering log shared by hooks and listeners.
type orderLog struct {
	mu      sync.Mutex
	entries []string
}

func (l *orderLog) add(entry string) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
}

func (l *orderLog) list() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.entries)
}

// countingQueues replaces the loop's queue getters, like upstream tests'
// getSteeringMessages/getFollowUpMessages config callbacks.
type countingQueues struct {
	mu            sync.Mutex
	steeringPolls int
	followUpPolls int
	steering      func(poll int) []AgentMessage
	followUp      func(poll int) []AgentMessage
}

func (q *countingQueues) install(cfg *agentLoopConfig) {
	cfg.getSteeringMessages = func() []AgentMessage {
		q.mu.Lock()
		q.steeringPolls++
		poll := q.steeringPolls
		q.mu.Unlock()
		if q.steering == nil {
			return nil
		}
		return q.steering(poll)
	}
	cfg.getFollowUpMessages = func() []AgentMessage {
		q.mu.Lock()
		q.followUpPolls++
		poll := q.followUpPolls
		q.mu.Unlock()
		if q.followUp == nil {
			return nil
		}
		return q.followUp(poll)
	}
}

func (q *countingQueues) polls() (steering, followUp int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.steeringPolls, q.followUpPolls
}

// runPrompt is upstream runAgentLoop: it runs a prompt with a loop config
// the test adjusted.
func runPrompt(t *testing.T, a *Agent, cfg agentLoopConfig, prompts ...AgentMessage) []AgentMessage {
	t.Helper()
	msgs, err := a.runPromptMessages(context.Background(), prompts, cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return msgs
}

func mustSend(t *testing.T, a *Agent, text string) []AgentMessage {
	t.Helper()
	msgs, err := a.Send(context.Background(), text)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	return msgs
}

// sendAsync runs Send on its own goroutine and closes the returned channel
// when it returns.
func sendAsync(t *testing.T, a *Agent, text string) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := a.Send(context.Background(), text); err != nil {
			t.Errorf("Send: %v", err)
		}
	}()
	return done
}

// awaitSignal waits for ch from any goroutine, reporting a timeout as a test
// error instead of stopping the test.
func awaitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for %s", what)
	}
}

// waitSignal waits for ch, failing the test after a timeout.
func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}
