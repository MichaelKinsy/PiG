package agent

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// ─── Fake provider for steering tests ─────────────────────────────────────────

// fakeProvider returns canned streaming responses in sequence.
type fakeProvider struct {
	mu        sync.Mutex
	callIndex int
	responses []fakeResponse
}

type fakeResponse struct {
	text      string
	toolCalls []fakeToolCall // if non-empty, emit tool call deltas
}

type fakeToolCall struct {
	id   string
	name string
	args string // JSON string
}

func (f *fakeProvider) ID() string   { return "fake" }
func (f *fakeProvider) Close() error { return nil }

func (f *fakeProvider) Stream(_ context.Context, _ ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	f.mu.Lock()
	index := f.callIndex
	f.callIndex++
	f.mu.Unlock()

	response := fakeResponse{text: "done"}
	if index < len(f.responses) {
		response = f.responses[index]
	}
	content := make([]ai.AssistantContentBlock, 0, 1+len(response.toolCalls))
	if response.text != "" {
		content = append(content, ai.TextContent{Text: response.text})
	}
	for _, call := range response.toolCalls {
		var arguments any
		if err := json.Unmarshal([]byte(call.args), &arguments); err != nil {
			return nil, err
		}
		object, ok := arguments.(map[string]any)
		if !ok {
			object = map[string]any{"value": arguments}
		}
		content = append(content, ai.ToolCall{ID: call.id, Name: call.name, Arguments: ai.JsonObject(object)})
	}
	reason := ai.StopReasonStop
	if len(response.toolCalls) > 0 {
		reason = ai.StopReasonToolUse
	}
	start := agentTestAssistant(nil, ai.StopReasonPending)
	final := agentTestAssistant(content, reason)
	return agentTestStream([]ai.AssistantMessageEvent{
		ai.StartEvent{Partial: start},
		ai.DoneEvent{Reason: reason, Message: final},
	}), nil
}

// ─── Fake tool ────────────────────────────────────────────────────────────────

type echoTool struct{}

func (echoTool) Name() string  { return "echo" }
func (echoTool) Label() string { return "" }
func (echoTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "echo", Description: "echo"}
}
func (echoTool) Execute(_ context.Context, _ string, params json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	return AgentToolResult{Content: string(params)}, nil
}
func (echoTool) ExecutionMode() ToolExecutionMode { return ToolModeSequential }

// slowEchoTool blocks on first execution until unblocked, then signals delay.
type slowEchoTool struct {
	delay   *sync.WaitGroup
	blockCh chan struct{}
	once    sync.Once
}

func (s *slowEchoTool) init() {
	s.once.Do(func() {
		s.blockCh = make(chan struct{})
	})
}

func (s *slowEchoTool) Name() string  { return "slow-echo" }
func (s *slowEchoTool) Label() string { return "" }
func (s *slowEchoTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "slow-echo", Description: "slow echo"}
}
func (s *slowEchoTool) Execute(_ context.Context, _ string, params json.RawMessage, _ ToolUpdateCallback) (AgentToolResult, error) {
	s.init()
	s.delay.Done() // signal tool execution started
	<-s.blockCh    // wait for unblock
	return AgentToolResult{Content: string(params)}, nil
}
func (s *slowEchoTool) ExecutionMode() ToolExecutionMode { return ToolModeSequential }

func (s *slowEchoTool) unblock() {
	s.init()
	close(s.blockCh)
}

// Ensure interfaces are satisfied.
var (
	_ ai.Provider = (*fakeProvider)(nil)
	_ AgentTool   = echoTool{}
	_ AgentTool   = (*slowEchoTool)(nil)
	_ io.Closer   = (*fakeProvider)(nil)
)

// ─── Steering tests ───────────────────────────────────────────────────────────

func TestSteering_MessageInjectedAfterToolExecution(t *testing.T) {
	// Scenario: agent calls echo tool, user steers after tool execution.
	// The steering message must be enqueued AFTER the first LLM call starts,
	// so we use a slow tool to create the timing window.
	var delay sync.WaitGroup
	delay.Add(1)
	slowTool := &slowEchoTool{delay: &delay}

	fp := &fakeProvider{
		responses: []fakeResponse{
			{toolCalls: []fakeToolCall{{id: "t1", name: "slow-echo", args: `"hello"`}}},
			{text: "response after steering"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
		Tools: []AgentTool{slowTool},
	})

	var wg sync.WaitGroup
	var msgs []AgentMessage
	var sendErr error

	wg.Go(func() {
		msgs, sendErr = agent.Send(context.Background(), "start")
	})

	// Wait for tool execution to start, then enqueue steering.
	delay.Wait()
	agent.Steer(userMsg("redirect to tests"))
	slowTool.unblock()

	wg.Wait()
	if sendErr != nil {
		t.Fatal(sendErr)
	}

	// Expected order:
	// 0: system tool declaration
	// 1: user "start"
	// 2: assistant (tool call)
	// 3: tool result
	// 4: user "redirect to tests" (steering, polled after tool execution)
	// 5: assistant "response after steering"
	if len(msgs) != 6 {
		for i, m := range msgs {
			t.Logf("msg[%d]: user=%v asst=%v tool=%v", i, m.User != nil, m.Assistant != nil, m.ToolResult != nil)
		}
		t.Fatalf("want 6 messages, got %d", len(msgs))
	}

	steerMsg := msgs[4]
	if steerMsg.User == nil {
		for i, m := range msgs {
			t.Logf("msg[%d]: user=%v asst=%v tool=%v", i, m.User != nil, m.Assistant != nil, m.ToolResult != nil)
		}
		t.Fatal("message 4 should be a user message (steering)")
	}
	if tc, ok := steerMsg.User.Content[0].(ai.TextContent); !ok || tc.Text != "redirect to tests" {
		t.Fatalf("steering text = %v, want 'redirect to tests'", steerMsg.User.Content[0])
	}

	if msgs[5].Assistant == nil {
		t.Fatal("message 5 should be assistant response")
	}
}

func TestFollowUp_ProcessedAfterAgentStops(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{text: "first response"},
			{text: "follow-up response"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
	})

	agent.FollowUp(userMsg("follow up question"))

	msgs, err := agent.Send(context.Background(), "initial question")
	if err != nil {
		t.Fatal(err)
	}

	// Expected: user, assistant, user(followup), assistant
	if len(msgs) < 4 {
		t.Fatalf("want >= 4 messages, got %d", len(msgs))
	}

	followUpMsg := msgs[2]
	if followUpMsg.User == nil {
		t.Fatal("message 2 should be a user message (follow-up)")
	}
	if tc, ok := followUpMsg.User.Content[0].(ai.TextContent); !ok || tc.Text != "follow up question" {
		t.Fatalf("follow-up text = %v, want 'follow up question'", followUpMsg.User.Content[0])
	}
}

func TestSteering_PriorityOverFollowUp(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{toolCalls: []fakeToolCall{{id: "t1", name: "echo", args: `"x"`}}},
			{text: "after steering"},
			{text: "after follow-up"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
		Tools: []AgentTool{echoTool{}},
	})

	agent.Steer(userMsg("steering first"))
	agent.FollowUp(userMsg("follow-up second"))

	msgs, err := agent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}

	var texts []string
	for _, m := range msgs {
		if m.User != nil {
			texts = append(texts, userMsgText(m))
		}
	}

	if len(texts) < 3 {
		t.Fatalf("want >= 3 user messages, got %d: %v", len(texts), texts)
	}
	if texts[0] != "go" || texts[1] != "steering first" || texts[2] != "follow-up second" {
		t.Fatalf("user messages = %v, want [go, steering first, follow-up second]", texts)
	}
}

func TestSteering_OneAtATimeMode(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{toolCalls: []fakeToolCall{{id: "t1", name: "echo", args: `"a"`}}},
			{toolCalls: []fakeToolCall{{id: "t2", name: "echo", args: `"b"`}}},
			{text: "final"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model:        &ai.Model{Provider: fp},
		Tools:        []AgentTool{echoTool{}},
		SteeringMode: QueueModeOneAtATime,
	})

	agent.Steer(userMsg("steer-1"))
	agent.Steer(userMsg("steer-2"))

	msgs, err := agent.Send(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}

	var userTexts []string
	for _, m := range msgs {
		if m.User != nil {
			userTexts = append(userTexts, userMsgText(m))
		}
	}

	if len(userTexts) != 3 {
		t.Fatalf("want 3 user messages [start, steer-1, steer-2], got %d: %v", len(userTexts), userTexts)
	}
	if userTexts[1] != "steer-1" || userTexts[2] != "steer-2" {
		t.Fatalf("steering order = %v, want [start, steer-1, steer-2]", userTexts)
	}
}

func TestSteering_AllMode(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{toolCalls: []fakeToolCall{{id: "t1", name: "echo", args: `"x"`}}},
			{text: "final"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model:        &ai.Model{Provider: fp},
		Tools:        []AgentTool{echoTool{}},
		SteeringMode: QueueModeAll,
	})

	agent.Steer(userMsg("steer-A"))
	agent.Steer(userMsg("steer-B"))

	msgs, err := agent.Send(context.Background(), "start")
	if err != nil {
		t.Fatal(err)
	}

	var userTexts []string
	for _, m := range msgs {
		if m.User != nil {
			userTexts = append(userTexts, userMsgText(m))
		}
	}

	if len(userTexts) != 3 {
		t.Fatalf("want 3 user messages, got %d: %v", len(userTexts), userTexts)
	}
	if userTexts[1] != "steer-A" || userTexts[2] != "steer-B" {
		t.Fatalf("got %v, want [start, steer-A, steer-B]", userTexts)
	}
}

func TestContinue_DrainsSteeringFirst(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{text: "after steering"},
			{text: "after follow-up"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
	})

	agent.SetMessages([]AgentMessage{
		userMsg("original"),
		{Assistant: &AssistantMessage{StopReason: "stop"}},
	})

	agent.Steer(userMsg("steering-msg"))
	agent.FollowUp(userMsg("followup-msg"))

	msgs, err := agent.Continue(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// Steering should appear before follow-up in the message history.
	steerIdx, followIdx := -1, -1
	for i, m := range msgs {
		if m.User != nil {
			txt := userMsgText(m)
			if txt == "steering-msg" {
				steerIdx = i
			}
			if txt == "followup-msg" {
				followIdx = i
			}
		}
	}
	if steerIdx == -1 {
		t.Fatal("steering message not found")
	}
	if followIdx != -1 && followIdx < steerIdx {
		t.Fatalf("follow-up (idx=%d) appeared before steering (idx=%d)", followIdx, steerIdx)
	}
}

func TestReset_ClearsBothQueues(t *testing.T) {
	agent := NewAgent(AgentOptions{})
	agent.SetMessages([]AgentMessage{userMsg("a")})
	agent.Steer(userMsg("s"))
	agent.FollowUp(userMsg("f"))

	if err := agent.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if len(agent.Messages()) != 0 {
		t.Fatal("messages not empty after reset")
	}
	if agent.HasPendingMessages() {
		t.Fatal("queues not empty after reset")
	}
}

func TestSteering_ConcurrentEnqueueDuringSend(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{toolCalls: []fakeToolCall{{id: "t1", name: "slow-echo", args: `"x"`}}},
			{text: "after steer"},
		},
	}

	var delay sync.WaitGroup
	delay.Add(1)
	slowTool := &slowEchoTool{delay: &delay}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
		Tools: []AgentTool{slowTool},
	})

	var wg sync.WaitGroup
	var msgs []AgentMessage
	var sendErr error

	wg.Go(func() {
		msgs, sendErr = agent.Send(context.Background(), "start")
	})

	delay.Wait()
	agent.Steer(userMsg("mid-flight steer"))
	slowTool.unblock()

	wg.Wait()
	if sendErr != nil {
		t.Fatal(sendErr)
	}

	found := false
	for _, m := range msgs {
		if m.User != nil && userMsgText(m) == "mid-flight steer" {
			found = true
		}
	}
	if !found {
		t.Fatal("concurrent steering message not found")
	}
}

func TestSteering_NoToolCallsInitialPoll(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{text: "first response"},
			{text: "second response"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
	})

	agent.Steer(userMsg("pre-queued steer"))

	msgs, err := agent.Send(context.Background(), "go")
	if err != nil {
		t.Fatal(err)
	}

	var userTexts []string
	for _, m := range msgs {
		if m.User != nil {
			userTexts = append(userTexts, userMsgText(m))
		}
	}

	if len(userTexts) < 2 {
		t.Fatalf("want >= 2 user messages, got %d: %v", len(userTexts), userTexts)
	}
	if userTexts[1] != "pre-queued steer" {
		t.Fatalf("second user message = %q, want 'pre-queued steer'", userTexts[1])
	}
}

func TestSteering_EmptyQueuesDoNothing(t *testing.T) {
	fp := &fakeProvider{
		responses: []fakeResponse{
			{text: "normal response"},
		},
	}

	agent := NewAgent(AgentOptions{
		Model: &ai.Model{Provider: fp},
	})

	msgs, err := agent.Send(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}

	if len(msgs) != 2 {
		t.Fatalf("want 2 messages (user + assistant), got %d", len(msgs))
	}
	if msgs[0].User == nil || msgs[1].Assistant == nil {
		t.Fatal("expected [user, assistant]")
	}
}
