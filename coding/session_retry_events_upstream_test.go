package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// streamScriptedMessageWithDeltas replays a scripted assistant message through the real faux provider, which streams
// start, block start, delta, block end and terminal events as upstream's faux provider does.
func streamScriptedMessageWithDeltas(ctx context.Context, request ai.TranscriptContext, options ai.StreamOptions, message *ai.AssistantMessage) (*ai.AssistantMessageEventStream, error) {
	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	var blocks []ai.FauxContentBlock
	for _, block := range message.Content {
		switch block := block.(type) {
		case ai.TextContent:
			blocks = append(blocks, ai.FauxText(block.Text))
		case ai.ThinkingContent:
			blocks = append(blocks, ai.FauxThinking(block.Thinking))
		case ai.ToolCall:
			blocks = append(blocks, ai.FauxToolCall(block.Name, map[string]any(block.Arguments), block.ID))
		}
	}
	provider.SetResponses([]ai.FauxResponseStep{ai.FauxStaticStep(ai.FauxResponse{Content: blocks, StopReason: string(message.StopReason), ErrorMessage: message.ErrorMessage})})
	return provider.Stream(ctx, request, options)
}

func fauxThinkingTextToolCall(thinking, text, tool string, arguments ai.JsonObject) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(), Content: []ai.AssistantContentBlock{
			ai.ThinkingContent{Thinking: thinking}, ai.TextContent{Text: text}, ai.ToolCall{ID: "call-" + tool, Name: tool, Arguments: arguments},
		}}
	}
}

// retryEchoTool is the suite's echo tool: it records the text it was called with and returns echo:<text>.
type retryEchoTool struct {
	mu   sync.Mutex
	runs []string
}

func (*retryEchoTool) Name() string  { return "echo" }
func (*retryEchoTool) Label() string { return "Echo" }
func (*retryEchoTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{Name: "echo", Description: "Echo text back", Parameters: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}, "required": []string{"text"}}}
}
func (*retryEchoTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }
func (tool *retryEchoTool) Execute(_ context.Context, _ string, params json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(params, &input); err != nil {
		return agent.AgentToolResult{}, err
	}
	tool.mu.Lock()
	tool.runs = append(tool.runs, input.Text)
	tool.mu.Unlock()
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "echo:" + input.Text}}, Details: map[string]any{"text": input.Text}}, nil
}
func (tool *retryEchoTool) ran() []string {
	tool.mu.Lock()
	defer tool.mu.Unlock()
	return slices.Clone(tool.runs)
}

const retryEventsSettings = `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}}`

// newRetryEventsHarness is the suite's createHarness: an empty in-memory manager, configured faux auth and a
// provider that streams block deltas.
func newRetryEventsHarness(t *testing.T, settings string, ext extension.Extension, tools []agent.AgentTool, responses ...scriptedResponse) *recoveryHarness {
	t.Helper()
	h := newBoundaryHarness(t, harnessOptions{settings: settings, extension: ext, tools: tools}, responses...)
	h.provider.streamDeltas = true
	return h
}

// eventLog records every event delivered to Subscribe listeners, in delivery order.
type eventLog struct {
	mu     sync.Mutex
	events []agent.AgentEvent
}

func recordSessionEvents(s *Session) *eventLog {
	log := &eventLog{}
	s.Subscribe(func(event agent.AgentEvent) {
		log.mu.Lock()
		log.events = append(log.events, event)
		log.mu.Unlock()
	})
	return log
}

func (l *eventLog) all() []agent.AgentEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

func eventsOf[T agent.AgentEvent](l *eventLog) []T {
	var out []T
	for _, event := range l.all() {
		if typed, ok := event.(T); ok {
			out = append(out, typed)
		}
	}
	return out
}

// normalizeEventOrder mirrors the upstream helper of the same name: message and tool events carry the role or tool
// name, and consecutive message_update events collapse into one.
func normalizeEventOrder(events []agent.AgentEvent) []string {
	var out []string
	for _, event := range events {
		label := ""
		switch event := event.(type) {
		case agent.AgentStartEvent:
			label = "agent_start"
		case agent.TurnStartEvent:
			label = "turn_start"
		case agent.TurnEndEvent:
			label = "turn_end"
		case agent.AgentEndEvent:
			label = "agent_end"
		case agent.AgentSettledEvent:
			label = "agent_settled"
		case agent.MessageStartEvent:
			label = "message_start:" + event.Message.Role()
		case agent.MessageEndEvent:
			label = "message_end:" + event.Message.Role()
		case agent.MessageUpdateEvent:
			label = "message_update"
		case agent.ToolExecutionStartEvent:
			label = "tool_execution_start:" + event.ToolName
		case agent.ToolExecutionEndEvent:
			label = "tool_execution_end:" + event.ToolName
		default:
			label = "unexpected:" + fmt.Sprintf("%T", event)
		}
		if label == "message_update" && len(out) > 0 && out[len(out)-1] == "message_update" {
			continue
		}
		out = append(out, label)
	}
	return out
}

// retrySummary renders auto_retry_start and auto_retry_end events as the upstream tests label them.
func retrySummary(log *eventLog) []string {
	var out []string
	for _, event := range log.all() {
		switch event := event.(type) {
		case agent.AutoRetryStartEvent:
			out = append(out, "start:"+strconv.Itoa(event.Attempt))
		case agent.AutoRetryEndEvent:
			if event.Success {
				out = append(out, "end:true")
			} else {
				out = append(out, "end:false")
			}
		}
	}
	return out
}

// sessionIsRetrying reads the unexported retry state because Session has no IsRetrying (upstream isRetrying is a deferred interface row).
func sessionIsRetrying(s *Session) bool {
	s.retryMu.Lock()
	defer s.retryMu.Unlock()
	return s.retryCancel != nil || s.retryAttempt.Load() != 0
}

func retryPrompt(t *testing.T, h *recoveryHarness, text string) {
	t.Helper()
	if _, err := h.session.Prompt(t.Context(), text, nil); err != nil {
		t.Fatal(err)
	}
}

func overloaded() scriptedResponse { return fauxError("overloaded_error") }

// upstream: packages/coding-agent/test/suite/agent-session-retry-events.test.ts
func TestUpstreamSessionRetryEvents(t *testing.T) {
	// :33
	t.Run("retries after a transient error and succeeds", func(t *testing.T) {
		h := newRetryEventsHarness(t, retryEventsSettings, extension.Extension{}, nil, overloaded(), fauxReply("recovered", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "test")
		if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "end:true"}) {
			t.Errorf("retry events=%v", got)
		}
		var willRetry []bool
		for _, end := range eventsOf[agent.AgentEndEvent](log) {
			willRetry = append(willRetry, end.WillRetry)
		}
		if !slices.Equal(willRetry, []bool{true, false}) {
			t.Errorf("agent_end willRetry=%v", willRetry)
		}
		if h.provider.callCount() != 2 || sessionIsRetrying(h.session) {
			t.Errorf("calls=%d retrying=%v", h.provider.callCount(), sessionIsRetrying(h.session))
		}
	})
	// :55
	t.Run("retries multiple transient failures and succeeds on the final attempt", func(t *testing.T) {
		h := newRetryEventsHarness(t, retryEventsSettings, extension.Extension{}, nil, overloaded(), overloaded(), fauxReply("success", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "test")
		if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "start:2", "end:true"}) {
			t.Errorf("retry events=%v", got)
		}
		if h.provider.callCount() != 3 {
			t.Errorf("calls=%d", h.provider.callCount())
		}
	})
	// :77 Regression #9340.
	t.Run("finalizes retry state when abort is requested after a retry attempt fails", func(t *testing.T) {
		h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":0}}`, extension.Extension{}, nil, overloaded(), overloaded())
		log := recordSessionEvents(h.session)
		errorCount := 0
		h.session.Subscribe(func(event agent.AgentEvent) {
			if end, ok := event.(agent.MessageEndEvent); ok && end.Message.Assistant != nil && end.Message.Assistant.StopReason == ai.StopReasonError {
				if errorCount++; errorCount == 2 {
					h.session.RequestAbort()
				}
			}
		})
		retryPrompt(t, h, "test")
		if attempt := h.session.retryAttempt.Load(); attempt != 0 {
			t.Errorf("retryAttempt=%d", attempt)
		}
		ends := eventsOf[agent.AgentEndEvent](log)
		if len(ends) == 0 || ends[len(ends)-1].WillRetry {
			t.Errorf("last agent_end=%v", ends)
		}
		retryEnds := eventsOf[agent.AutoRetryEndEvent](log)
		if len(retryEnds) == 0 {
			t.Fatal("missing auto_retry_end")
		}
		if last := retryEnds[len(retryEnds)-1]; last.Success || last.Attempt != 1 || last.FinalError != "Retry cancelled" {
			t.Errorf("last auto_retry_end=%+v", last)
		}
	})
	// :102
	t.Run("exhausts max retries and emits a failure event", func(t *testing.T) {
		h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":2,"baseDelayMs":1}}`, extension.Extension{}, nil, overloaded(), overloaded(), overloaded())
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "test")
		if got := retrySummary(log); !slices.Equal(got, []string{"start:1", "start:2", "end:false"}) {
			t.Errorf("retry events=%v", got)
		}
		var willRetry []bool
		for _, end := range eventsOf[agent.AgentEndEvent](log) {
			willRetry = append(willRetry, end.WillRetry)
		}
		if !slices.Equal(willRetry, []bool{true, true, false}) {
			t.Errorf("agent_end willRetry=%v", willRetry)
		}
		if h.provider.callCount() != 3 || sessionIsRetrying(h.session) {
			t.Errorf("calls=%d retrying=%v", h.provider.callCount(), sessionIsRetrying(h.session))
		}
	})
	// :125 The 40ms delay is the upstream stimulus that yields the message_end handler, not a wait for completion.
	t.Run("prompt waits for retry completion even when assistant message_end handling is delayed", func(t *testing.T) {
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"message_end": {func(args ...any) (any, error) {
			if message, ok := args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage); ok && message.Assistant != nil {
				time.Sleep(40 * time.Millisecond)
			}
			return nil, nil
		}}}}
		h := newRetryEventsHarness(t, retryEventsSettings, ext, nil, overloaded(), fauxReply("recovered", ai.StopReasonStop, 0))
		retryPrompt(t, h, "test")
		if h.provider.callCount() != 2 || sessionIsRetrying(h.session) {
			t.Errorf("calls=%d retrying=%v", h.provider.callCount(), sessionIsRetrying(h.session))
		}
	})
	// :150
	t.Run("does not retry when retry is disabled", func(t *testing.T) {
		h := newRetryEventsHarness(t, `{"retry":{"enabled":false}}`, extension.Extension{}, nil, overloaded())
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "test")
		if h.provider.callCount() != 1 || len(eventsOf[agent.AutoRetryStartEvent](log)) != 0 {
			t.Errorf("calls=%d retry starts=%d", h.provider.callCount(), len(eventsOf[agent.AutoRetryStartEvent](log)))
		}
	})
	// :161
	t.Run("does not retry non-retryable errors", func(t *testing.T) {
		h := newRetryEventsHarness(t, retryEventsSettings, extension.Extension{}, nil, fauxError("invalid_api_key"))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "test")
		if h.provider.callCount() != 1 || len(eventsOf[agent.AutoRetryStartEvent](log)) != 0 {
			t.Errorf("calls=%d retry starts=%d", h.provider.callCount(), len(eventsOf[agent.AutoRetryStartEvent](log)))
		}
	})
	// :172
	t.Run("cancels retry sleep when abortRetry is called", func(t *testing.T) {
		h := newRetryEventsHarness(t, `{"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":100}}`, extension.Extension{}, nil, overloaded())
		log := recordSessionEvents(h.session)
		sawRetryStart := make(chan struct{})
		var once sync.Once
		h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.AutoRetryStartEvent); ok {
				once.Do(func() { close(sawRetryStart) })
			}
		})
		done := make(chan error, 1)
		go func() {
			_, err := h.session.Prompt(t.Context(), "test", nil)
			done <- err
		}()
		select {
		case <-sawRetryStart:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("auto_retry_start was not published")
		}
		h.session.AbortRetry()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("prompt did not return after abortRetry")
		}
		var finalErrors []string
		for _, end := range eventsOf[agent.AutoRetryEndEvent](log) {
			finalErrors = append(finalErrors, end.FinalError)
		}
		if !slices.Contains(finalErrors, "Retry cancelled") {
			t.Errorf("auto_retry_end final errors=%v", finalErrors)
		}
		if sessionIsRetrying(h.session) || h.provider.callCount() != 1 {
			t.Errorf("retrying=%v calls=%d", sessionIsRetrying(h.session), h.provider.callCount())
		}
	})
	// :196
	t.Run("waits for the full loop when retry recovery produces tool calls", func(t *testing.T) {
		echo := &retryEchoTool{}
		h := newRetryEventsHarness(t, retryEventsSettings, extension.Extension{}, []agent.AgentTool{echo},
			overloaded(), fauxToolCallOnly("echo", ai.JsonObject{"text": "hello"}), fauxReply("final answer", ai.StopReasonStop, 0), fauxReply("follow-up done", ai.StopReasonStop, 0))
		retryPrompt(t, h, "test")
		if h.provider.callCount() != 3 || !slices.Equal(echo.ran(), []string{"hello"}) || h.session.IsStreaming() {
			t.Errorf("calls=%d runs=%v streaming=%v", h.provider.callCount(), echo.ran(), h.session.IsStreaming())
		}
		retryPrompt(t, h, "follow-up")
		if h.provider.callCount() != 4 {
			t.Errorf("calls after follow-up=%d", h.provider.callCount())
		}
	})
	// :229
	t.Run("emits extension events before public event subscribers", func(t *testing.T) {
		var mu sync.Mutex
		var order []string
		record := func(label string) {
			mu.Lock()
			order = append(order, label)
			mu.Unlock()
		}
		ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"message_start": {func(args ...any) (any, error) {
				record("extension:message_start:" + args[0].(extension.MessageStartEvent).Message.(agent.AgentMessage).Role())
				return nil, nil
			}},
			"message_end": {func(args ...any) (any, error) {
				record("extension:message_end:" + args[0].(extension.MessageEndEvent).Message.(agent.AgentMessage).Role())
				return nil, nil
			}},
		}}
		h := newRetryEventsHarness(t, "{}", ext, nil, fauxReply("done", ai.StopReasonStop, 0))
		h.session.Subscribe(func(event agent.AgentEvent) {
			switch event := event.(type) {
			case agent.MessageStartEvent:
				record("public:message_start:" + event.Message.Role())
			case agent.MessageEndEvent:
				record("public:message_end:" + event.Message.Role())
			}
		})
		retryPrompt(t, h, "hi")
		want := []string{
			"extension:message_start:system", "public:message_start:system", "extension:message_end:system", "public:message_end:system",
			"extension:message_start:user", "public:message_start:user", "extension:message_end:user", "public:message_end:user",
			"extension:message_start:assistant", "public:message_start:assistant", "extension:message_end:assistant", "public:message_end:assistant",
		}
		mu.Lock()
		defer mu.Unlock()
		if !slices.Equal(order, want) {
			t.Errorf("order=\n%s\nwant=\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
		}
	})
	// :269
	t.Run("emits the expected event order for a single prompt", func(t *testing.T) {
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply("hello", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "hi")
		want := []string{"agent_start", "turn_start", "message_start:system", "message_end:system", "message_start:user", "message_end:user", "message_start:assistant", "message_update", "message_end:assistant", "turn_end", "agent_end", "agent_settled"}
		if got := normalizeEventOrder(log.all()); !slices.Equal(got, want) {
			t.Errorf("event order=%v\nwant=%v", got, want)
		}
	})
	// :292
	t.Run("emits the expected event order for a tool call turn", func(t *testing.T) {
		echo := &retryEchoTool{}
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, []agent.AgentTool{echo}, fauxToolCallOnly("echo", ai.JsonObject{"text": "hello"}), fauxReply("done", ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "hi")
		if !slices.Equal(echo.ran(), []string{"hello"}) {
			t.Errorf("tool runs=%v", echo.ran())
		}
		want := []string{
			"agent_start", "turn_start", "message_start:system", "message_end:system", "message_start:user", "message_end:user",
			"message_start:assistant", "message_update", "message_end:assistant",
			"tool_execution_start:echo", "tool_execution_end:echo", "message_start:toolResult", "message_end:toolResult", "turn_end",
			"turn_start", "message_start:assistant", "message_update", "message_end:assistant", "turn_end", "agent_end", "agent_settled",
		}
		if got := normalizeEventOrder(log.all()); !slices.Equal(got, want) {
			t.Errorf("event order=%v\nwant=%v", got, want)
		}
	})
	// :340
	t.Run("emits streaming deltas for text, thinking, and tool calls in message_update events", func(t *testing.T) {
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxThinkingTextToolCall("plan", "answer", "echo", ai.JsonObject{"text": "hello"}))
		log := recordSessionEvents(h.session)
		_, _ = h.session.Prompt(t.Context(), "hi", nil) // upstream: prompt("hi").catch(() => {}); the unregistered echo tool ends the loop with an error.
		types := map[ai.AssistantEventType]bool{}
		for _, update := range eventsOf[agent.MessageUpdateEvent](log) {
			types[update.AssistantMessageEvent.EventType()] = true
		}
		for _, want := range []ai.AssistantEventType{ai.EventThinkingDelta, ai.EventTextDelta, ai.EventToolCallDelta} {
			if !types[want] {
				t.Errorf("message_update lacks %s; saw %v", want, types)
			}
		}
	})
	// :360
	t.Run("emits agent_end for error responses", func(t *testing.T) {
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxError("broken"))
		log := recordSessionEvents(h.session)
		retryPrompt(t, h, "hi")
		events := log.all()
		if n := len(eventsOf[agent.AgentEndEvent](log)); n != 1 {
			t.Errorf("agent_end count=%d", n)
		}
		if _, ok := events[len(events)-1].(agent.AgentSettledEvent); !ok {
			t.Errorf("last event=%T", events[len(events)-1])
		}
	})
	// :371
	t.Run("emits agent_end for aborted runs and persists the aborted assistant message", func(t *testing.T) {
		h := newRetryEventsHarness(t, "{}", extension.Extension{}, nil, fauxReply(strings.Repeat("x", 20_000), ai.StopReasonStop, 0))
		log := recordSessionEvents(h.session)
		sawUpdate := make(chan struct{})
		var once sync.Once
		h.session.Subscribe(func(event agent.AgentEvent) {
			if _, ok := event.(agent.MessageUpdateEvent); ok {
				once.Do(func() { close(sawUpdate) })
			}
		})
		done := make(chan error, 1)
		go func() {
			_, err := h.session.Prompt(t.Context(), "hi", nil)
			done <- err
		}()
		select {
		case <-sawUpdate:
		case <-time.After(testbudget.Wait(t)):
			t.Fatal("message_update was not published")
		}
		if err := h.session.Abort(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		events := log.all()
		if n := len(eventsOf[agent.AgentEndEvent](log)); n != 1 {
			t.Errorf("agent_end count=%d", n)
		}
		if _, ok := events[len(events)-1].(agent.AgentSettledEvent); !ok {
			t.Errorf("last event=%T", events[len(events)-1])
		}
		messages := h.session.Messages()
		last := messages[len(messages)-1]
		if last.Role() != "assistant" || last.Assistant.StopReason != ai.StopReasonAborted {
			t.Errorf("last message role=%s stop=%v", last.Role(), last.Assistant)
		}
	})
}

func fauxToolCallOnly(tool string, arguments ai.JsonObject) scriptedResponse {
	return func([]ai.Message) *ai.AssistantMessage {
		return &ai.AssistantMessage{Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Timestamp: time.Now().UnixMilli(),
			Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call-" + tool, Name: tool, Arguments: arguments}}}
	}
}
