package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// Ports packages/ai/test/anthropic-message-threads.test.ts (Anthropic message threads, Pi #11545).

const threadsBeta = "message-threads-2026-08-12"

const threadsGrammarError = "The compiled grammar is too large, which would cause performance issues. Simplify your tool schemas or reduce the number of strict tools."

type threadWire = map[string]any

type threadCaptured struct {
	body  threadWire
	betas []string
}

func (c threadCaptured) thread() map[string]any {
	thread, _ := c.body["thread"].(map[string]any)
	return thread
}

func (c threadCaptured) messages() []any { return c.body["messages"].([]any) }

type threadReplySpec struct {
	id         string
	model      string
	text       string
	thinking   *string
	toolCalls  []threadToolCall
	stopReason string
	// end is an event to emit instead of finishing normally: "error_event" or "no_message_stop".
	end string
}

type threadToolCall struct {
	id, name string
	input    map[string]any
}

func threadSSE(events []map[string]any, tail string) *http.Response {
	var body strings.Builder
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(&body, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	body.WriteString(tail)
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body.String()))}
}

func threadReply(request threadCaptured, spec threadReplySpec) *http.Response {
	id, model, text := spec.id, spec.model, spec.text
	if id == "" {
		id = "msg_x"
	}
	if model == "" {
		model, _ = request.body["model"].(string)
	}
	if text == "" {
		text = "answer"
	}
	events := []map[string]any{{"type": "message_start", "message": map[string]any{"id": id, "model": model, "usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}}}
	index := 0
	if spec.thinking != nil {
		events = append(events,
			map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}},
			map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "thinking_delta", "thinking": *spec.thinking}},
			map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "signature_delta", "signature": "sig"}},
			map[string]any{"type": "content_block_stop", "index": index},
		)
		index++
	}
	events = append(events,
		map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": text}},
		map[string]any{"type": "content_block_stop", "index": index},
	)
	index++
	for _, call := range spec.toolCalls {
		input, _ := json.Marshal(call.input)
		events = append(events,
			map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "tool_use", "id": call.id, "name": call.name, "input": map[string]any{}}},
			map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
			map[string]any{"type": "content_block_stop", "index": index},
		)
		index++
	}
	if spec.end == "error_event" {
		return threadSSE(events, `event: error`+"\n"+`data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`+"\n\n")
	}
	stopReason := spec.stopReason
	if stopReason == "" {
		stopReason = "end_turn"
		if len(spec.toolCalls) > 0 {
			stopReason = "tool_use"
		}
	}
	events = append(events, map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stopReason}, "usage": map[string]any{"output_tokens": 1}})
	if spec.end != "no_message_stop" {
		events = append(events, map[string]any{"type": "message_stop"})
	}
	return threadSSE(events, "")
}

func threadErrorResponse(status int, message, errorCode string, headers map[string]string) *http.Response {
	errorType := "invalid_request_error"
	switch status {
	case 404:
		errorType = "not_found_error"
	case 429:
		errorType = "rate_limit_error"
	}
	inner := map[string]any{"type": errorType, "message": message}
	if errorCode != "" {
		inner["details"] = map[string]any{"error_code": errorCode}
	}
	body, _ := json.Marshal(map[string]any{"type": "error", "error": inner})
	response := &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(string(body)))}
	for name, value := range headers {
		response.Header.Set(name, value)
	}
	return response
}

type threadResponder func(threadCaptured) *http.Response

// threadFingerprint holds the fields Anthropic requires to stay the same within a thread (observed live).
func threadFingerprint(c threadCaptured) string {
	_, hasFallbacks := c.body["fallbacks"]
	data, _ := json.Marshal([]any{c.body["model"], c.body["thinking"], c.body["output_config"], c.body["tool_choice"], hasFallbacks, c.betas})
	return string(data)
}

var threadSessionCounter struct {
	sync.Mutex
	n int
}

// threadHarness is a fake Anthropic endpoint: it records request bodies and beta lists, and answers from a queue or with a plain reply. Like
// the real server, it rejects a continue whose fixed fields differ from the thread's.
type threadHarness struct {
	mu           sync.Mutex
	requests     []threadCaptured
	queue        []threadResponder
	fingerprints map[string]string
	sessionID    string
	client       *http.Client
}

func newThreadHarness(t *testing.T) *threadHarness {
	t.Helper()
	threadSessionCounter.Lock()
	threadSessionCounter.n++
	n := threadSessionCounter.n
	threadSessionCounter.Unlock()
	h := &threadHarness{fingerprints: map[string]string{}, sessionID: fmt.Sprintf("thread-test-%d", n)}
	t.Cleanup(func() { _ = CleanupSessionResources("") })
	h.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(request.Body)
		var body threadWire
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v", err)
		}
		var betas []string
		if header := request.Header.Get("anthropic-beta"); header != "" {
			betas = strings.Split(header, ",")
		}
		captured := threadCaptured{body: body, betas: betas}
		h.mu.Lock()
		h.requests = append(h.requests, captured)
		count := len(h.requests)
		var next threadResponder
		if len(h.queue) > 0 {
			next, h.queue = h.queue[0], h.queue[1:]
		}
		h.mu.Unlock()
		if next != nil {
			return next(captured), nil
		}
		id := fmt.Sprintf("msg_%d", count)
		thread := captured.thread()
		previous, _ := thread["previous_message_id"].(string)
		expected := threadFingerprint(captured)
		if previous != "" {
			h.mu.Lock()
			expected = h.fingerprints[previous]
			h.mu.Unlock()
		}
		if expected != threadFingerprint(captured) {
			return threadErrorResponse(400, "fixed field changed", "thread_fingerprint_mismatch", nil), nil
		}
		if thread != nil {
			h.mu.Lock()
			h.fingerprints[id] = expected
			h.mu.Unlock()
		}
		return threadReply(captured, threadReplySpec{id: id}), nil
	})}
	return h
}

func (h *threadHarness) push(responders ...threadResponder) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.queue = append(h.queue, responders...)
}

func (h *threadHarness) captured() []threadCaptured {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]threadCaptured(nil), h.requests...)
}

func (h *threadHarness) request(t *testing.T, index int) threadCaptured {
	t.Helper()
	requests := h.captured()
	if index >= len(requests) {
		t.Fatalf("request %d not sent; %d requests", index, len(requests))
	}
	return requests[index]
}

func (h *threadHarness) types() []string {
	var types []string
	for _, request := range h.captured() {
		if thread := request.thread(); thread != nil {
			types = append(types, thread["type"].(string))
		} else {
			types = append(types, "none")
		}
	}
	return types
}

func threadTool(name string) ToolSchema {
	return ToolSchema{Name: name, Description: "Tool " + name, Parameters: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []any{"value"}}}
}

func threadUser(text string) Message { return UserMessage{Content: UserText(text), Timestamp: 1} }

func threadToolResult(id, name string) Message {
	return ToolResultMessage{ToolCallID: id, ToolName: name, Content: []ToolResultMessageContent{TextContent{Text: "result"}}, Timestamp: 1}
}

// threadModel is the test's baseModel: the anthropic provider on api.anthropic.com.
type threadModel struct {
	id        string
	reasoning bool
	compat    *ModelCompat
}

func (m threadModel) config() AnthropicConfig {
	id := m.id
	if id == "" {
		id = "claude-test"
	}
	return AnthropicConfig{
		Model: id, ProviderID: "anthropic", BaseURL: "https://api.anthropic.com", Compat: m.compat,
		ModelMetadata: &Model{ID: id, DisplayName: "Test Claude", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 100}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, BaseURL: "https://api.anthropic.com", Reasoning: m.reasoning}},
	}
}

type threadTurnInput struct {
	model        threadModel
	systemPrompt string
	tools        []ToolSchema
	noTools      bool
	options      func(*StreamOptions)
	apiKey       string
	ctx          context.Context
}

func (h *threadHarness) turn(t *testing.T, messages []Message, input threadTurnInput) *AssistantMessage {
	t.Helper()
	prompt := input.systemPrompt
	if prompt == "" {
		prompt = "You are a test assistant."
	}
	tools := input.tools
	if tools == nil && !input.noTools {
		tools = []ToolSchema{threadTool("lookup")}
	}
	config := input.model.config()
	config.APIKey = input.apiKey
	if config.APIKey == "" {
		config.APIKey = "test-key"
	}
	retries := 0
	options := StreamOptions{Fetch: h.client, SessionID: h.sessionID, MaxRetries: &retries}
	if input.options != nil {
		input.options(&options)
	}
	ctx := input.ctx
	if ctx == nil {
		ctx = t.Context()
	}
	stream, err := NewAnthropicProvider(config).Stream(ctx, NormalizeContext(Context{SystemPrompt: prompt, Tools: tools, Messages: messages}), options)
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

// say runs one turn and appends the reply to the conversation.
func (h *threadHarness) say(t *testing.T, messages *[]Message, input threadTurnInput) *AssistantMessage {
	t.Helper()
	result := h.turn(t, *messages, input)
	*messages = append(*messages, *result)
	return result
}

func withThreadOptions(f func(*StreamOptions)) threadTurnInput { return threadTurnInput{options: f} }

func wantThread(t *testing.T, request threadCaptured, want map[string]any) {
	t.Helper()
	if got := request.thread(); !reflect.DeepEqual(got, want) {
		t.Fatalf("thread = %v, want %v", got, want)
	}
}

func cacheEphemeral() map[string]any { return map[string]any{"type": "ephemeral"} }

func TestAnthropicMessageThreadsCreateThenContinue(t *testing.T) {
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{})
	messages = append(messages, threadUser("second"))
	result := h.say(t, &messages, threadTurnInput{})

	if result.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
	}
	create, next := h.request(t, 0), h.request(t, 1)
	wantThread(t, create, map[string]any{"type": "create"})
	if create.body["system"] == nil || create.body["tools"] == nil {
		t.Fatalf("create request lacks system or tools: %v", create.body)
	}
	wantThread(t, next, map[string]any{"type": "continue", "previous_message_id": "msg_1"})
	if _, ok := next.body["system"]; ok {
		t.Fatal("continue request has system")
	}
	if _, ok := next.body["tools"]; ok {
		t.Fatal("continue request has tools")
	}
	// The first user message had string content turned into a cached block on request 1, and plain string content on request 2; the cache
	// marker moved to the new message.
	if got, want := create.messages()[0].(map[string]any)["content"], []any{map[string]any{"type": "text", "text": "first", "cache_control": cacheEphemeral()}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first message content = %v, want %v", got, want)
	}
	wantMessages := []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "second", "cache_control": cacheEphemeral()}}}}
	if !reflect.DeepEqual(next.messages(), wantMessages) {
		t.Fatalf("continue messages = %v, want %v", next.messages(), wantMessages)
	}
	if !contains(create.betas, threadsBeta) {
		t.Fatalf("create betas = %v", create.betas)
	}
	if !reflect.DeepEqual(next.betas, create.betas) {
		t.Fatalf("continue betas = %v, want %v", next.betas, create.betas)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestAnthropicMessageThreadsContinueAfterToolCalls(t *testing.T) {
	h := newThreadHarness(t)
	h.push(func(request threadCaptured) *http.Response {
		return threadReply(request, threadReplySpec{id: "msg_tool", toolCalls: []threadToolCall{{id: "toolu_1", name: "lookup", input: map[string]any{"value": "x"}}}})
	})
	messages := []Message{threadUser("look it up")}
	first := h.say(t, &messages, threadTurnInput{})
	if first.StopReason != StopReasonToolUse {
		t.Fatalf("first stopReason = %v (%s)", first.StopReason, first.ErrorMessage)
	}
	messages = append(messages, threadToolResult("toolu_1", "lookup"))
	h.say(t, &messages, threadTurnInput{})

	next := h.request(t, 1)
	wantThread(t, next, map[string]any{"type": "continue", "previous_message_id": "msg_tool"})
	if len(next.messages()) != 1 {
		t.Fatalf("continue messages = %v, want one without placeholder results", next.messages())
	}
	message := next.messages()[0].(map[string]any)
	block := message["content"].([]any)[0].(map[string]any)
	if message["role"] != "user" || block["type"] != "tool_result" || block["tool_use_id"] != "toolu_1" {
		t.Fatalf("continue message = %v", message)
	}
}

func threadEffortOrRole(messages []any) []string {
	var out []string
	for _, message := range messages {
		m := message.(map[string]any)
		if config, ok := m["output_config"].(map[string]any); ok {
			if effort, ok := config["effort"].(string); ok {
				out = append(out, effort)
				continue
			}
		}
		out = append(out, m["role"].(string))
	}
	return out
}

func TestAnthropicMessageThreadsContinueWhenEffortChanges(t *testing.T) {
	model := threadModel{reasoning: true, compat: &ModelCompat{ForceAdaptiveThinking: new(true), SupportsMidConvoEffort: new(true)}}
	h := newThreadHarness(t)
	thinking := "thought"
	h.push(func(request threadCaptured) *http.Response {
		return threadReply(request, threadReplySpec{id: "msg_a", thinking: &thinking})
	})
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{model: model, options: func(o *StreamOptions) { o.ThinkingEnabled, o.Effort = new(true), AnthropicEffortHigh }})
	messages = append(messages, threadUser("second"))
	h.say(t, &messages, threadTurnInput{model: model, options: func(o *StreamOptions) { o.ThinkingEnabled, o.Effort = new(true), AnthropicEffortLow }})

	if got, want := threadEffortOrRole(h.request(t, 0).messages()), []string{"user", "high"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first request messages = %v, want %v", got, want)
	}
	next := h.request(t, 1)
	wantThread(t, next, map[string]any{"type": "continue", "previous_message_id": "msg_a"})
	if got, want := threadEffortOrRole(next.messages()), []string{"user", "low"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("continue messages = %v, want %v", got, want)
	}
}

func TestAnthropicMessageThreadsContinueAfterNativeToolChange(t *testing.T) {
	model := threadModel{compat: &ModelCompat{SupportsMidConvoSystemMessages: new(true), SupportsMidConvoToolChanges: new(true)}}
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{model: model})
	messages = append(messages, SystemMessage{Content: SystemText("Update"), ToolsAdded: []ToolSchema{threadTool("other")}, Timestamp: 2}, threadUser("second"))
	h.say(t, &messages, threadTurnInput{model: model})

	next := h.request(t, 1)
	if next.thread()["type"] != "continue" {
		t.Fatalf("thread = %v", next.thread())
	}
	var roles []string
	for _, message := range next.messages() {
		roles = append(roles, message.(map[string]any)["role"].(string))
	}
	if !reflect.DeepEqual(roles, []string{"user", "system"}) {
		t.Fatalf("continue roles = %v", roles)
	}
	content, _ := json.Marshal(next.messages()[1].(map[string]any)["content"])
	if !strings.Contains(string(content), "tool_addition") {
		t.Fatalf("system message content = %s", content)
	}
}

func TestAnthropicMessageThreadsNewThreadWhenSettingChanges(t *testing.T) {
	reasoning := threadModel{reasoning: true}
	budget := func(tokens int) func(*StreamOptions) {
		return func(o *StreamOptions) { o.ThinkingEnabled, o.ThinkingBudgetTokens = new(true), new(tokens) }
	}
	for _, tc := range []struct {
		name          string
		before, after threadTurnInput
	}{
		{"system prompt", threadTurnInput{}, threadTurnInput{systemPrompt: "Different prompt."}},
		{"tools", threadTurnInput{}, threadTurnInput{tools: []ToolSchema{threadTool("lookup"), threadTool("other")}}},
		{"model", threadTurnInput{}, threadTurnInput{model: threadModel{id: "claude-other"}}},
		{"tool choice", threadTurnInput{}, withThreadOptions(func(o *StreamOptions) { o.ToolChoice = "any" })},
		{"cache retention", threadTurnInput{}, withThreadOptions(func(o *StreamOptions) { o.CacheRetention = CacheRetentionLong })},
		{"thinking level", threadTurnInput{model: reasoning, options: budget(1024)}, threadTurnInput{model: reasoning, options: budget(2048)}},
	} {
		t.Run("creates a new thread when the "+tc.name+" changes", func(t *testing.T) {
			h := newThreadHarness(t)
			messages := []Message{threadUser("first")}
			h.say(t, &messages, tc.before)
			messages = append(messages, threadUser("second"))
			h.say(t, &messages, tc.after)
			wantThread(t, h.request(t, 1), map[string]any{"type": "create"})
			if n := len(h.request(t, 1).messages()); n != 3 {
				t.Fatalf("messages = %d, want 3", n)
			}
		})
	}
}

func TestAnthropicMessageThreadsNewThreadWithoutMessagesBeyondPrefix(t *testing.T) {
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{})
	// The request ends with the assistant message, which Anthropic would reject; the test observes the thread choice only.
	h.turn(t, messages, threadTurnInput{ctx: requestvalidity.Allow(t.Context())})
	wantThread(t, h.request(t, 1), map[string]any{"type": "create"})
}

func TestAnthropicMessageThreadsNewThreadAfterRejection(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
	}{{"thread_not_found", 404}, {"thread_already_continued", 400}, {"thread_continue_in_flight", 400}, {"thread_fingerprint_mismatch", 400}} {
		t.Run("starts a new thread with the full request after "+tc.code, func(t *testing.T) {
			h := newThreadHarness(t)
			messages := []Message{threadUser("first")}
			h.say(t, &messages, threadTurnInput{})
			messages = append(messages, threadUser("second"))
			h.push(func(threadCaptured) *http.Response {
				return threadErrorResponse(tc.status, "thread rejected", tc.code, nil)
			})
			payloads := 0
			result := h.say(t, &messages, withThreadOptions(func(o *StreamOptions) {
				o.OnPayload = func(any, *Model) (any, error) { payloads++; return nil, nil }
			}))

			if result.StopReason != StopReasonStop {
				t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
			}
			if payloads != 1 {
				t.Fatalf("OnPayload called %d times, want 1", payloads)
			}
			if n := len(h.captured()); n != 3 {
				t.Fatalf("requests = %d, want 3", n)
			}
			rejected, resend := h.request(t, 1), h.request(t, 2)
			if rejected.thread()["type"] != "continue" {
				t.Fatalf("rejected thread = %v", rejected.thread())
			}
			wantThread(t, resend, map[string]any{"type": "create"})
			if len(resend.messages()) != 3 || resend.body["system"] == nil {
				t.Fatalf("resend = %v", resend.body)
			}
			if !reflect.DeepEqual(resend.betas, rejected.betas) {
				t.Fatalf("resend betas = %v, want %v", resend.betas, rejected.betas)
			}
			if len(result.Diagnostics) != 1 || result.Diagnostics[0].Type != "anthropic_thread_fallback" || result.Diagnostics[0].Details["reason"] != tc.code {
				t.Fatalf("diagnostics = %+v", result.Diagnostics)
			}

			messages = append(messages, threadUser("third"))
			h.say(t, &messages, threadTurnInput{})
			wantThread(t, h.request(t, 3), map[string]any{"type": "continue", "previous_message_id": "msg_3"})
		})
	}
}

func TestAnthropicMessageThreadsOffAfterUnsupported(t *testing.T) {
	for _, tc := range []struct{ name, code, message, reason string }{
		{"thread_unsupported_request", "thread_unsupported_request", "rejected", "thread_unsupported_request"},
		{"a 400 without error code about the thread field", "", "thread: Extra inputs are not permitted", "400"},
	} {
		t.Run("turns threads off for the session after "+tc.name+" on continue", func(t *testing.T) {
			h := newThreadHarness(t)
			messages := []Message{threadUser("first")}
			h.say(t, &messages, threadTurnInput{})
			messages = append(messages, threadUser("second"))
			h.push(func(threadCaptured) *http.Response { return threadErrorResponse(400, tc.message, tc.code, nil) })
			result := h.say(t, &messages, threadTurnInput{})

			if result.StopReason != StopReasonStop {
				t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
			}
			if len(result.Diagnostics) == 0 || result.Diagnostics[0].Type != "anthropic_thread_fallback" || result.Diagnostics[0].Details["reason"] != tc.reason {
				t.Fatalf("diagnostics = %+v", result.Diagnostics)
			}
			messages = append(messages, threadUser("third"))
			h.say(t, &messages, threadTurnInput{})
			later := h.request(t, 3)
			if _, ok := later.body["thread"]; ok {
				t.Fatalf("later request has a thread: %v", later.thread())
			}
			// The resend was accepted with the threads beta, so the beta list stays the same.
			if !reflect.DeepEqual(later.betas, h.request(t, 0).betas) {
				t.Fatalf("later betas = %v, want %v", later.betas, h.request(t, 0).betas)
			}
		})
	}
}

func TestAnthropicMessageThreadsResendFailureKeepsThreadsOn(t *testing.T) {
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{})
	messages = append(messages, threadUser("second"))
	h.push(
		func(threadCaptured) *http.Response {
			return threadErrorResponse(400, "thread: Extra inputs are not permitted", "", nil)
		},
		func(threadCaptured) *http.Response { return threadErrorResponse(400, "Real problem", "", nil) },
	)
	failed := h.turn(t, messages, threadTurnInput{})
	if failed.StopReason != StopReasonError || !strings.Contains(failed.ErrorMessage, "Real problem") {
		t.Fatalf("stopReason = %v, error = %q", failed.StopReason, failed.ErrorMessage)
	}
	if n := len(h.captured()); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
	h.turn(t, messages, threadTurnInput{})
	wantThread(t, h.request(t, 3), map[string]any{"type": "create"})
}

func TestAnthropicMessageThreadsPassesOnErrorsNotAboutTheThread(t *testing.T) {
	for _, tc := range []struct {
		status  int
		message string
	}{{400, "prompt is too long: 250000 tokens > 200000 maximum"}, {404, "model: claude-test"}} {
		t.Run(fmt.Sprintf("passes a %d that is not about the thread on without resending", tc.status), func(t *testing.T) {
			h := newThreadHarness(t)
			messages := []Message{threadUser("first")}
			h.say(t, &messages, threadTurnInput{})
			messages = append(messages, threadUser("second"))
			h.push(func(threadCaptured) *http.Response { return threadErrorResponse(tc.status, tc.message, "", nil) })
			failed := h.turn(t, messages, threadTurnInput{})
			if failed.StopReason != StopReasonError || !strings.Contains(failed.ErrorMessage, tc.message) {
				t.Fatalf("stopReason = %v, error = %q", failed.StopReason, failed.ErrorMessage)
			}
			if n := len(h.captured()); n != 2 {
				t.Fatalf("requests = %d, want 2", n)
			}
			if failed.Diagnostics != nil {
				t.Fatalf("diagnostics = %+v", failed.Diagnostics)
			}
			// Threads stay on: after a fix such as compaction, the next request starts a new thread.
			h.turn(t, messages, threadTurnInput{})
			wantThread(t, h.request(t, 2), map[string]any{"type": "create"})
		})
	}
}

func TestAnthropicMessageThreadsLeaveStrictToolRejectionsToStrictFallback(t *testing.T) {
	model := threadModel{compat: &ModelCompat{SupportsStrictTools: new(true)}}
	strictTool := threadTool("lookup")
	strictTool.ConstrainedSampling = &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: "prefer"}
	h := newThreadHarness(t)
	h.push(func(threadCaptured) *http.Response { return threadErrorResponse(400, threadsGrammarError, "", nil) })
	result := h.turn(t, []Message{threadUser("first")}, threadTurnInput{model: model, tools: []ToolSchema{strictTool}})
	if result.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
	}
	if got := h.types(); !reflect.DeepEqual(got, []string{"create", "create"}) {
		t.Fatalf("thread types = %v", got)
	}
	var diagnostics []string
	for _, diagnostic := range result.Diagnostics {
		diagnostics = append(diagnostics, diagnostic.Type)
	}
	if !reflect.DeepEqual(diagnostics, []string{"anthropic_strict_tools_fallback"}) {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
}

func TestAnthropicMessageThreadsRetriedContinueFindsThreadInFlight(t *testing.T) {
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{})
	messages = append(messages, threadUser("second"))
	h.push(
		func(threadCaptured) *http.Response {
			return threadErrorResponse(503, "overloaded", "", map[string]string{"retry-after-ms": "0"})
		},
		func(threadCaptured) *http.Response {
			return threadErrorResponse(400, "in flight", "thread_continue_in_flight", nil)
		},
	)
	result := h.say(t, &messages, withThreadOptions(func(o *StreamOptions) { o.MaxRetries = new(1) }))
	if result.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
	}
	if got, want := h.types(), []string{"create", "continue", "continue", "create"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("thread types = %v, want %v", got, want)
	}
}

func TestAnthropicMessageThreadsExplicitBetaHeader(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header *string
	}{{"without the threads beta", new("fine-grained-tool-streaming-2025-05-14")}, {"set to null", nil}} {
		t.Run("fails once, then turns threads off, with an explicit anthropic-beta header "+tc.name, func(t *testing.T) {
			h := newThreadHarness(t)
			respond := func(request threadCaptured) *http.Response {
				if _, ok := request.body["thread"]; ok && !contains(request.betas, threadsBeta) {
					return threadErrorResponse(400, "thread: Extra inputs are not permitted", "", nil)
				}
				return threadReply(request, threadReplySpec{id: fmt.Sprintf("msg_%d", len(h.captured()))})
			}
			h.push(respond, respond, respond)
			options := func(o *StreamOptions) { o.Headers = ProviderHeaders{"anthropic-beta": tc.header} }
			messages := []Message{threadUser("first")}
			result := h.say(t, &messages, withThreadOptions(options))
			if result.StopReason != StopReasonStop {
				t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
			}
			messages = append(messages, threadUser("second"))
			h.say(t, &messages, withThreadOptions(options))
			if got, want := h.types(), []string{"create", "none", "none"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("thread types = %v, want %v", got, want)
			}
		})
	}
}

func TestAnthropicMessageThreadsDoNotRecordUncleanReplies(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec *threadReplySpec // nil aborts the reply
	}{
		{"an aborted reply", nil},
		{"a stream error", &threadReplySpec{end: "error_event"}},
		{"a stream that ends early", &threadReplySpec{end: "no_message_stop"}},
		{"a refusal", &threadReplySpec{stopReason: "refusal"}},
		{"a fallback model reply", &threadReplySpec{model: "claude-fallback"}},
	} {
		t.Run("does not record "+tc.name, func(t *testing.T) {
			h := newThreadHarness(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			input := threadTurnInput{}
			if tc.spec != nil {
				spec := *tc.spec
				spec.id = "msg_bad"
				h.push(func(request threadCaptured) *http.Response { return threadReply(request, spec) })
			} else {
				// The caller aborts once the reply has started.
				h.push(func(request threadCaptured) *http.Response {
					response := threadReply(request, threadReplySpec{id: "msg_bad"})
					response.Body = io.NopCloser(io.MultiReader(strings.NewReader(threadStartOnly(request)), threadAbortReader{cancel}))
					return response
				})
				input.ctx = ctx
			}
			messages := []Message{threadUser("first")}
			first := h.turn(t, messages, input)
			messages = append(messages, *first, threadUser("second"))
			h.turn(t, messages, threadTurnInput{})
			wantThread(t, h.request(t, 1), map[string]any{"type": "create"})
		})
	}
}

func threadStartOnly(request threadCaptured) string {
	model, _ := request.body["model"].(string)
	data, _ := json.Marshal(map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_bad", "model": model, "usage": map[string]any{"input_tokens": 10, "output_tokens": 0}}})
	return fmt.Sprintf("event: message_start\ndata: %s\n\n", data)
}

// threadAbortReader aborts the request, then fails the read as a canceled request does.
type threadAbortReader struct{ cancel context.CancelFunc }

func (r threadAbortReader) Read([]byte) (int, error) {
	r.cancel()
	return 0, context.Canceled
}

func TestAnthropicMessageThreadsNotUsedWithCacheRetentionNone(t *testing.T) {
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, withThreadOptions(func(o *StreamOptions) { o.CacheRetention = CacheRetentionNone }))
	request := h.request(t, 0)
	if _, ok := request.body["thread"]; ok {
		t.Fatalf("thread = %v", request.thread())
	}
	if contains(request.betas, threadsBeta) {
		t.Fatalf("betas = %v", request.betas)
	}
}

func TestAnthropicMessageThreadsMatchClaudeCodeToolNamesWithOAuth(t *testing.T) {
	h := newThreadHarness(t)
	h.push(func(request threadCaptured) *http.Response {
		return threadReply(request, threadReplySpec{id: "msg_oauth", toolCalls: []threadToolCall{{id: "toolu_1", name: "Read", input: map[string]any{"value": "x"}}}})
	})
	input := threadTurnInput{tools: []ToolSchema{threadTool("read")}, apiKey: "sk-ant-oat-test"}
	messages := []Message{threadUser("read it")}
	first := h.say(t, &messages, input)
	var call *ToolCall
	for _, block := range first.Content {
		if c, ok := block.(ToolCall); ok {
			call = &c
		}
	}
	if call == nil || call.Name != "read" {
		t.Fatalf("tool call = %+v", call)
	}
	messages = append(messages, threadToolResult("toolu_1", "read"))
	h.say(t, &messages, input)
	wantThread(t, h.request(t, 1), map[string]any{"type": "continue", "previous_message_id": "msg_oauth"})
}

func TestAnthropicMessageThreadsCacheWarmingReplayKeepsTheThread(t *testing.T) {
	h := newThreadHarness(t)
	messages := []Message{threadUser("first")}
	h.say(t, &messages, threadTurnInput{})
	h.turn(t, messages[:1], withThreadOptions(func(o *StreamOptions) { o.MaxTokens, o.DisableContinuation = 1, true }))
	messages = append(messages, threadUser("second"))
	h.say(t, &messages, threadTurnInput{})

	real, warm, next := h.request(t, 0), h.request(t, 1), h.request(t, 2)
	if _, ok := warm.body["thread"]; ok {
		t.Fatalf("warm request has a thread: %v", warm.thread())
	}
	if warm.body["max_tokens"] != float64(1) {
		t.Fatalf("warm max_tokens = %v", warm.body["max_tokens"])
	}
	if !reflect.DeepEqual(warm.messages(), real.messages()) {
		t.Fatalf("warm messages = %v, want %v", warm.messages(), real.messages())
	}
	if !reflect.DeepEqual(warm.betas, real.betas) {
		t.Fatalf("warm betas = %v, want %v", warm.betas, real.betas)
	}
	wantThread(t, next, map[string]any{"type": "continue", "previous_message_id": "msg_1"})
}

// PiG-only: the default and the compat override of supportsMessageThreads (anthropic-messages.ts getAnthropicCompat).
func TestAnthropicMessageThreadsEndpointSupport(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider string
		baseURL  string
		compat   *ModelCompat
		want     bool
	}{
		{"anthropic on api.anthropic.com", "anthropic", "https://api.anthropic.com", nil, true},
		{"a gateway", "anthropic", "https://gateway.invalid", nil, false},
		{"another provider on api.anthropic.com", "other", "https://api.anthropic.com", nil, false},
		{"a gateway with the compat on", "anthropic", "https://gateway.invalid", &ModelCompat{SupportsMessageThreads: new(true)}, true},
		{"api.anthropic.com with the compat off", "anthropic", "https://api.anthropic.com", &ModelCompat{SupportsMessageThreads: new(false)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newThreadHarness(t)
			config := threadModel{compat: tc.compat}.config()
			config.ProviderID, config.BaseURL, config.APIKey = tc.provider, tc.baseURL, "test-key"
			retries := 0
			stream, err := NewAnthropicProvider(config).Stream(t.Context(), NormalizeContext(Context{Messages: []Message{threadUser("first")}}), StreamOptions{Fetch: h.client, SessionID: h.sessionID, MaxRetries: &retries})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result.StopReason != StopReasonStop {
				t.Fatalf("stopReason = %v (%s)", result.StopReason, result.ErrorMessage)
			}
			request := h.request(t, 0)
			_, sent := request.body["thread"]
			if sent != tc.want || contains(request.betas, threadsBeta) != tc.want {
				t.Fatalf("thread sent = %v, betas = %v, want threads %v", sent, request.betas, tc.want)
			}
		})
	}
}
