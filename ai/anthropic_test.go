package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// helper to create a test SSE server and run the provider against it.
func runAnthropicSSE(t *testing.T, sseData string) []AssistantMessageEvent {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q, want %q", got, "test-key")
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want %q", got, "2023-06-01")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, sseData)
	}))
	t.Cleanup(srv.Close)

	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:     "test-key",
		Model:      "claude-sonnet-4-20250514",
		BaseURL:    srv.URL,
		ProviderID: "anthropic",
	})

	transcript := NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("You are helpful")},
		UserMessage{Content: UserText("Hello")},
	}})
	stream, err := p.Stream(context.Background(), transcript, StreamOptions{MaxTokens: 1024})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}

	var events []AssistantMessageEvent
	for event := range stream.Events(context.Background()) {
		events = append(events, event)
	}
	return events
}

func anthropicEventTypes(events []AssistantMessageEvent) []AssistantEventType {
	types := make([]AssistantEventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.EventType())
	}
	return types
}

func anthropicTerminal(t *testing.T, events []AssistantMessageEvent) *AssistantMessage {
	t.Helper()
	for _, event := range events {
		switch event := event.(type) {
		case DoneEvent:
			return event.Message
		case ErrorEvent:
			return event.Error
		}
	}
	t.Fatal("Anthropic stream has no terminal event")
	return nil
}

func TestAnthropicSSE_TextOnly(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_123","usage":{"input_tokens":25,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world!"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`
	events := runAnthropicSSE(t, sse)

	// Expect: start, text(""), text("Hello"), text(" world!"), done
	var texts []string
	var gotStart, gotDone bool
	for _, event := range events {
		switch event := event.(type) {
		case StartEvent:
			gotStart = true
		case TextDeltaEvent:
			texts = append(texts, event.Delta)
		case DoneEvent:
			gotDone = true
			if event.Message.Usage.Input != 25 {
				t.Errorf("input = %d, want 25", event.Message.Usage.Input)
			}
			if event.Message.Usage.Output != 12 {
				t.Errorf("output = %d, want 12", event.Message.Usage.Output)
			}
		}
	}
	if !gotStart {
		t.Error("missing start event")
	}
	if !gotDone {
		t.Error("missing done event")
	}
	joined := strings.Join(texts, "")
	if !strings.Contains(joined, "Hello world!") {
		t.Errorf("text deltas = %q, want to contain 'Hello world!'", joined)
	}
}

func TestAnthropicSSE_ToolCallNoDoubleArgsOnStop(t *testing.T) {
	// Regression: content_block_stop for tool_use previously re-emitted
	// ab.toolArgs.String() as an ArgsDelta, which the agent loop appended
	// to the accumulated args, producing `{...}{...}` JSON that failed
	// parsing. Upstream Pi finalises in-place and emits no synthetic
	// delta. This test asserts the concatenated ArgsDelta equals the
	// original streamed JSON: not doubled.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_dup","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_dup","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\": \"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{}}

event: message_stop
data: {"type":"message_stop"}

`
	events := runAnthropicSSE(t, sse)
	message := anthropicTerminal(t, events)
	tool := message.Content[0].(ToolCall)
	if tool.Arguments["command"] != "ls" {
		t.Fatalf("tool = %#v", tool)
	}
	wantTypes := []AssistantEventType{EventStart, EventToolCallStart, EventToolCallDelta, EventToolCallEnd, EventDone}
	if got := anthropicEventTypes(events); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	delta := events[2].(ToolCallDeltaEvent)
	if delta.ContentIndex != 0 || delta.Delta != `{"command": "ls"}` {
		t.Fatalf("tool delta = %#v", delta)
	}
	end := events[3].(ToolCallEndEvent)
	if end.ContentIndex != 0 || !reflect.DeepEqual(end.ToolCall.Arguments, JsonObject{"command": "ls"}) {
		t.Fatalf("tool end = %#v", end)
	}
}

func TestAnthropicSSE_ParallelToolCallsTrackIndex(t *testing.T) {
	// Regression: Anthropic stream previously left Index=0 on every
	// ToolCallDelta, causing parallel tool calls to collide in the agent's
	// toolMap (wrong tool name paired with wrong args). This test
	// asserts each tool block carries its Anthropic content-block index.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_par","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_a","name":"read"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_b","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{}}

event: message_stop
data: {"type":"message_stop"}

`
	events := runAnthropicSSE(t, sse)
	message := anthropicTerminal(t, events)
	if len(message.Content) != 2 {
		t.Fatalf("content = %#v", message.Content)
	}
	first := message.Content[0].(ToolCall)
	second := message.Content[1].(ToolCall)
	if first.Name != "read" || first.Arguments["path"] != "a" || second.Name != "bash" || second.Arguments["command"] != "ls" {
		t.Fatalf("content = %#v", message.Content)
	}
	wantTypes := []AssistantEventType{
		EventStart,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventDone,
	}
	if got := anthropicEventTypes(events); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	firstDelta := events[2].(ToolCallDeltaEvent)
	secondDelta := events[5].(ToolCallDeltaEvent)
	if firstDelta.ContentIndex != 0 || firstDelta.Delta != `{"path":"a"}` || secondDelta.ContentIndex != 1 || secondDelta.Delta != `{"command":"ls"}` {
		t.Fatalf("tool deltas = %#v, %#v", firstDelta, secondDelta)
	}
}

func TestAnthropicSSE_ToolCallsAfterThinkingAndText(t *testing.T) {
	// Regression: when thinking (index=0) and text (index=1) blocks precede
	// tool_use blocks (index=2, 3), the raw Anthropic content_block index was
	// used as ToolCallDelta.Index. The agent core's collection loop iterates
	// i=0..len(toolMap)-1, expecting 0-based sequential keys. With raw
	// indexes {2,3}, toolMap[0] and toolMap[1] didn't exist → zero tool calls
	// collected. Fix: Anthropic provider tracks a toolCallSeqIdx counter that
	// yields 0-based sequential indexes for tool calls only.
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_think","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think..."}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Reading files."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: content_block_start
data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"toolu_r","name":"read"}}

event: content_block_delta
data: {"type":"content_block_delta","index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":2}

event: content_block_start
data: {"type":"content_block_start","index":3,"content_block":{"type":"tool_use","id":"toolu_b","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":3,"delta":{"type":"input_json_delta","partial_json":"{\"command\":\"ls\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":3}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{}}

event: message_stop
data: {"type":"message_stop"}

`
	events := runAnthropicSSE(t, sse)
	message := anthropicTerminal(t, events)
	if len(message.Content) != 4 {
		t.Fatalf("content = %#v", message.Content)
	}
	thinking := message.Content[0].(ThinkingContent)
	first := message.Content[2].(ToolCall)
	second := message.Content[3].(ToolCall)
	if thinking.Thinking != "Let me think..." || thinking.ThinkingSignature != "" || first.Name != "read" || second.Name != "bash" {
		t.Fatalf("content = %#v", message.Content)
	}
	wantTypes := []AssistantEventType{
		EventStart,
		EventThinkingStart, EventThinkingDelta, EventThinkingEnd,
		EventTextStart, EventTextDelta, EventTextEnd,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventDone,
	}
	if got := anthropicEventTypes(events); !reflect.DeepEqual(got, wantTypes) {
		t.Fatalf("event types = %v, want %v", got, wantTypes)
	}
	firstDelta := events[8].(ToolCallDeltaEvent)
	secondDelta := events[11].(ToolCallDeltaEvent)
	if firstDelta.ContentIndex != 2 || secondDelta.ContentIndex != 3 {
		t.Fatalf("tool content indexes = %d, %d, want 2, 3", firstDelta.ContentIndex, secondDelta.ContentIndex)
	}
}

func TestAnthropicSSE_ToolCall(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_456","usage":{"input_tokens":50,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_01","name":"bash"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"command\""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":": \"ls -la\"}"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":30}}

event: message_stop
data: {"type":"message_stop"}

`
	message := anthropicTerminal(t, runAnthropicSSE(t, sse))
	tool := message.Content[0].(ToolCall)
	if tool.ID != "toolu_01" || tool.Name != "bash" {
		t.Fatalf("tool = %#v", tool)
	}
}

func TestAnthropicSSE_Thinking(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_789","usage":{"input_tokens":30,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me think..."}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig123"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Here is my answer."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}

event: message_stop
data: {"type":"message_stop"}

`
	message := anthropicTerminal(t, runAnthropicSSE(t, sse))
	thinking := message.Content[0].(ThinkingContent)
	text := message.Content[1].(TextContent)
	if thinking.Thinking != "Let me think..." || thinking.ThinkingSignature != "sig123" || text.Text != "Here is my answer." {
		t.Fatalf("content = %#v", message.Content)
	}
}

func TestAnthropicSSE_RedactedThinking(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_red","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"redacted_thinking","data":"opaque_data"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"Answer."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}

event: message_stop
data: {"type":"message_stop"}

`
	message := anthropicTerminal(t, runAnthropicSSE(t, sse))
	thinking := message.Content[0].(ThinkingContent)
	if !thinking.Redacted || thinking.ThinkingSignature != "opaque_data" {
		t.Fatalf("thinking = %#v", thinking)
	}
}

func TestAnthropicSSE_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(529)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	}))
	t.Cleanup(srv.Close)

	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:  "test-key",
		Model:   "claude-sonnet-4-20250514",
		BaseURL: srv.URL,
	})

	_, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}), StreamOptions{MaxTokens: 1024})
	if err == nil {
		t.Fatal("expected error for 529 response")
	}
	if !strings.Contains(err.Error(), "overloaded_error") {
		t.Errorf("error = %q, want to contain 'overloaded_error'", err.Error())
	}
	if !strings.Contains(err.Error(), "Overloaded") {
		t.Errorf("error = %q, want to contain 'Overloaded'", err.Error())
	}
}

func TestAnthropicSSE_Usage(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_u","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":50,"cache_creation_input_tokens":10,"output_tokens_details":{"thinking_tokens":3}}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5,"output_tokens_details":{"thinking_tokens":4}}}

event: message_stop
data: {"type":"message_stop"}

`
	usage := anthropicTerminal(t, runAnthropicSSE(t, sse)).Usage
	if usage.Input != 100 {
		t.Errorf("input = %d, want 100", usage.Input)
	}
	if usage.Output != 5 {
		t.Errorf("output = %d, want 5", usage.Output)
	}
	if usage.CacheRead != 50 {
		t.Errorf("cache_read = %d, want 50", usage.CacheRead)
	}
	if usage.CacheWrite != 10 {
		t.Errorf("cache_write = %d, want 10", usage.CacheWrite)
	}
	if usage.Reasoning == nil || *usage.Reasoning != 4 {
		t.Errorf("reasoning = %v, want 4", usage.Reasoning)
	}
}

func TestAnthropicConvertMessages(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserText("Hello")},
		AssistantMessage{Content: []AssistantContentBlock{
			TextContent{Text: "Hi there"},
			ToolCall{ID: "t1", Name: "bash", Arguments: JsonObject{"command": "ls"}},
		}},
		ToolResultMessage{ToolCallID: "t1", Content: []ToolResultMessageContent{TextContent{Text: "file.txt"}}},
	}

	out := anthConvertMessages(messages, false, false, false)
	if len(out) != 3 {
		t.Fatalf("got %d messages, want 3", len(out))
	}
	if out[0].Role != "user" {
		t.Errorf("msg[0].role = %q, want user", out[0].Role)
	}
	if out[1].Role != "assistant" {
		t.Errorf("msg[1].role = %q, want assistant", out[1].Role)
	}
	// Tool result should be a user message
	if out[2].Role != "user" {
		t.Errorf("msg[2].role = %q, want user (tool_result)", out[2].Role)
	}
}

// Thinking-block conversion must mirror upstream anthropic.ts convertMessages:
// redacted -> redacted_thinking, empty-text -> dropped, empty/invalid signature
// -> text (or preserved thinking when allowEmptySignature), else thinking+sig.
// The empty-text+signature case is the github-copilot/claude regression:
// thinkingDisplay "omitted" persists an empty `thinking` field with a valid
// signature, and re-sending it verbatim yields HTTP 400
// "messages.N.content.0.thinking.thinking: Field required".
func TestAnthConvertMessages_ThinkingBlocks(t *testing.T) {
	const sig = "EpQMCmMIDxgCKkABCDEF" // valid-looking Anthropic signature (not JSON)
	type want struct {
		dropped bool // assistant message produces no output block
		block   anthContentBlock
	}
	tests := []struct {
		name                string
		in                  ThinkingContent
		allowEmptySignature bool
		want                want
	}{
		{
			name: "empty thinking with signature is preserved",
			in:   ThinkingContent{Thinking: "", ThinkingSignature: sig},
			want: want{block: anthContentBlock{Type: "thinking", Thinking: "", Signature: sig}},
		},
		{
			name: "whitespace-only thinking with signature is preserved",
			in:   ThinkingContent{Thinking: "   \n", ThinkingSignature: sig},
			want: want{block: anthContentBlock{Type: "thinking", Thinking: "   \n", Signature: sig}},
		},
		{
			name: "empty thinking with empty signature is dropped",
			in:   ThinkingContent{Thinking: "", ThinkingSignature: ""},
			want: want{dropped: true},
		},
		{
			name: "redacted block becomes redacted_thinking carrying the signature",
			in:   ThinkingContent{Thinking: "[Reasoning redacted]", ThinkingSignature: "opaque-data", Redacted: true},
			want: want{block: anthContentBlock{Type: "redacted_thinking", Data: "opaque-data"}},
		},
		{
			name: "normal thinking with signature is preserved",
			in:   ThinkingContent{Thinking: "reasoning", ThinkingSignature: sig},
			want: want{block: anthContentBlock{Type: "thinking", Thinking: "reasoning", Signature: sig}},
		},
		{
			name: "thinking with empty signature converts to text by default",
			in:   ThinkingContent{Thinking: "reasoning", ThinkingSignature: ""},
			want: want{block: anthContentBlock{Type: "text", Text: "reasoning"}},
		},
		{
			name:                "thinking with empty signature preserved when allowEmptySignature",
			in:                  ThinkingContent{Thinking: "reasoning", ThinkingSignature: ""},
			allowEmptySignature: true,
			want:                want{block: anthContentBlock{Type: "thinking", Thinking: "reasoning", Signature: ""}},
		},
		// Cross-model thinking (e.g. an OpenAI reasoning item whose signature is a
		// JSON object) is converted to text upstream of this converter, in
		// agent.NormalizeMessages. See
		// TestNormalizeMessages_CrossModelThinking for that behavior; the
		// converter only ever sees same-model thinking.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []Message{AssistantMessage{Content: []AssistantContentBlock{tt.in}}}
			out := anthConvertMessages(messages, false, tt.allowEmptySignature, false)
			if tt.want.dropped {
				if len(out) != 0 {
					t.Fatalf("want assistant message dropped, got %d messages: %+v", len(out), out)
				}
				return
			}
			if len(out) != 1 {
				t.Fatalf("want 1 message, got %d: %+v", len(out), out)
			}
			blocks, ok := out[0].Content.([]anthContentBlock)
			if !ok || len(blocks) != 1 {
				t.Fatalf("want 1 content block, got %#v", out[0].Content)
			}
			if blocks[0] != tt.want.block {
				t.Errorf("block = %#v, want %#v", blocks[0], tt.want.block)
			}
		})
	}
}

// Real-session regression: signed empty thinking remains replayable while its
// text and tool-call siblings survive in order.
func TestAnthConvertMessages_EmptyThinkingDroppedSiblingsKept(t *testing.T) {
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{ThinkingSignature: "EpQMvalidsig"},
		TextContent{Text: "answer"},
		ToolCall{ID: "t1", Name: "bash", Arguments: JsonObject{"command": "ls"}},
	}}}
	out := anthConvertMessages(messages, false, false, false)
	if len(out) != 1 {
		t.Fatalf("want 1 message, got %d", len(out))
	}
	blocks := out[0].Content.([]anthContentBlock)
	if len(blocks) != 3 {
		t.Fatalf("want 3 blocks (thinking, text, tool_use), got %d: %#v", len(blocks), blocks)
	}
	if blocks[0].Type != "thinking" || blocks[1].Type != "text" || blocks[2].Type != "tool_use" {
		t.Errorf("want [thinking, text, tool_use], got [%s, %s, %s]", blocks[0].Type, blocks[1].Type, blocks[2].Type)
	}
}

func TestNormalizeAnthropicToolCallID(t *testing.T) {
	// Upstream normalizeToolCallId: id.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, 64)
	tests := []struct {
		in, want string
	}{
		{"toolu_abc123", "toolu_abc123"},                    // already valid
		{"call_abc123", "call_abc123"},                      // OpenAI completions format
		{"call_abc|item_def", "call_abc_item_def"},          // OpenAI responses pipe
		{"a.b:c/d", "a_b_c_d"},                              // various invalid chars
		{strings.Repeat("x", 100), strings.Repeat("x", 64)}, // truncate to 64
		{"", ""}, // empty
	}
	for _, tt := range tests {
		got := normalizeAnthropicToolCallID(tt.in)
		if got != tt.want {
			t.Errorf("normalizeAnthropicToolCallID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAnthropicConvertMessages_NormalizesToolCallIDs(t *testing.T) {
	// Cross-provider scenario: OpenAI Responses API tool call IDs contain pipes.
	// The Anthropic API requires ^[a-zA-Z0-9_-]+$.
	messages := []Message{
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call_abc|item_def", Name: "read", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "call_abc|item_def", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
	}
	out := anthConvertMessages(messages, false, false, false)
	if len(out) != 2 {
		t.Fatalf("got %d messages, want 2", len(out))
	}
	// Assistant message tool_use block should have normalized ID.
	blocks := out[0].Content.([]anthContentBlock)
	if blocks[0].ID != "call_abc_item_def" {
		t.Errorf("tool_use ID = %q, want %q", blocks[0].ID, "call_abc_item_def")
	}
	// User message tool_result block should have matching normalized ID.
	results := out[1].Content.([]anthContentBlock)
	if results[0].ToolUseID != "call_abc_item_def" {
		t.Errorf("tool_result tool_use_id = %q, want %q", results[0].ToolUseID, "call_abc_item_def")
	}
}

func TestAnthropicConvertToolsCompat(t *testing.T) {
	tools := []ToolSchema{{
		Name:        "bash",
		Description: "run bash",
		Parameters: map[string]any{
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
			"required":   []string{"command"},
		},
	}}
	cache := &anthCacheControl{Type: "ephemeral"}
	out, err := anthConvertTools(tools, false, true, false, cache)
	if err != nil {
		t.Fatalf("anthConvertTools: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	if out[0].EagerInputStream != true {
		t.Fatalf("eager_input_streaming = %#v, want true", out[0].EagerInputStream)
	}
	if out[0].CacheControl == nil || out[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("cache_control = %#v, want ephemeral", out[0].CacheControl)
	}

	out, err = anthConvertTools(tools, false, false, false, nil)
	if err != nil {
		t.Fatalf("anthConvertTools: %v", err)
	}
	if out[0].EagerInputStream != nil {
		t.Fatalf("eager_input_streaming = %#v, want omitted", out[0].EagerInputStream)
	}
	if out[0].CacheControl != nil {
		t.Fatalf("cache_control = %#v, want omitted", out[0].CacheControl)
	}
}

// TestAnthropicConvertToolsStrict ports the strict-versus-legacy schema cases
// from upstream anthropic-eager-tool-input-compat.test.ts.
func TestAnthropicConvertToolsStrict(t *testing.T) {
	legacy := ToolSchema{
		Name: "lookup", Description: "Look up a value",
		Parameters: map[string]any{
			"type": "object", "title": "LookupInput", "additionalProperties": false,
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []any{"value"},
		},
	}
	legacyTools, err := anthConvertTools([]ToolSchema{legacy}, false, true, true, nil)
	if err != nil {
		t.Fatalf("legacy tool: %v", err)
	}
	if legacyTools[0].Strict != nil {
		t.Fatalf("legacy strict = %#v, want omitted", legacyTools[0].Strict)
	}
	if _, exists := legacyTools[0].InputSchema["title"]; exists {
		t.Fatalf("legacy input schema retained title: %#v", legacyTools[0].InputSchema)
	}
	if _, exists := legacyTools[0].InputSchema["additionalProperties"]; exists {
		t.Fatalf("legacy input schema retained additionalProperties: %#v", legacyTools[0].InputSchema)
	}

	strict := legacy
	strict.Parameters = map[string]any{
		"type": "object", "title": "StrictLookupInput",
		"properties": map[string]any{
			"value":    map[string]any{"type": "string"},
			"optional": map[string]any{"type": "number"},
		},
		"required": []any{"value"},
	}
	strict.ConstrainedSampling = &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"}
	strictTools, err := anthConvertTools([]ToolSchema{strict}, false, true, true, nil)
	if err != nil {
		t.Fatalf("strict tool: %v", err)
	}
	if strictTools[0].Strict == nil || !*strictTools[0].Strict {
		t.Fatalf("strict = %#v, want true", strictTools[0].Strict)
	}
	if strictTools[0].InputSchema["title"] != "StrictLookupInput" || strictTools[0].InputSchema["additionalProperties"] != false {
		t.Fatalf("strict input schema = %#v", strictTools[0].InputSchema)
	}
	optional := strictTools[0].InputSchema["properties"].(map[string]any)["optional"].(map[string]any)
	if _, ok := optional["anyOf"]; !ok {
		t.Fatalf("strict optional property = %#v, want nullable anyOf", optional)
	}
}

func TestAnthropicStrictEmptyToolWireRequiredArray(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	supportsStrict := true
	provider := NewAnthropicProvider(AnthropicConfig{
		APIKey: "test-key", Model: "custom", ProviderID: "test-anthropic", BaseURL: server.URL,
		Compat: &AnthropicMessagesCompat{SupportsStrictTools: &supportsStrict},
	})
	transcript := NormalizeContext(Context{Messages: []Message{
		SystemMessage{ToolsAdded: []ToolSchema{{
			Name: "no_args", Description: "No arguments", Parameters: map[string]any{"type": "object"},
			ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"},
		}}},
		UserMessage{Content: UserText("call it")},
	}})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = stream.Result()

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["strict"] != true {
		t.Fatalf("strict = %#v, want true", tool["strict"])
	}
	inputSchema := tool["input_schema"].(map[string]any)
	required, ok := inputSchema["required"].([]any)
	if !ok || len(required) != 0 {
		t.Fatalf("input_schema.required = %#v, want []", inputSchema["required"])
	}
}

func TestAnthropicNativeToolChangesApplyStrictSchemasToInitialAndDeferredTools(t *testing.T) {
	strictTool := func(name string) ToolSchema {
		return ToolSchema{
			Name: name, Description: name,
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"optional": map[string]any{"type": "string"},
			}},
			ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "require"},
		}
	}
	initial := strictTool("initial")
	later := strictTool("later")
	messages := []Message{
		SystemMessage{ToolsAdded: []ToolSchema{initial}},
		UserMessage{Content: UserText("one")},
		SystemMessage{ToolsAdded: []ToolSchema{later}},
	}
	params := anthropicParams{nativeToolChanges: true, tools: []ToolSchema{initial, later}}
	tools, err := anthropicRequestTools(messages, params, []ToolSchema{initial}, false, true, true, nil)
	if err != nil {
		t.Fatalf("anthropicRequestTools: %v", err)
	}
	if len(tools) != 3 {
		t.Fatalf("tools = %#v, want initial, placeholder, deferred", tools)
	}
	for _, index := range []int{0, 2} {
		tool := tools[index]
		if tool.Strict == nil || !*tool.Strict || tool.InputSchema["additionalProperties"] != false {
			t.Fatalf("tool[%d] = %#v, want strict converted schema", index, tool)
		}
	}
	if !tools[2].DeferLoading {
		t.Fatalf("deferred tool = %#v, want defer_loading", tools[2])
	}
}

func TestAnthropicToolChoiceWireMapping(t *testing.T) {
	cases := []struct {
		name string
		give any
		want map[string]any
	}{
		{name: "auto", give: "auto", want: map[string]any{"type": "auto"}},
		{name: "any", give: "any", want: map[string]any{"type": "any"}},
		{name: "none", give: "none", want: map[string]any{"type": "none"}},
		{name: "named tool", give: map[string]any{"type": "tool", "name": "lookup"}, want: map[string]any{"type": "tool", "name": "lookup"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()

			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "custom", ProviderID: "test-anthropic", BaseURL: server.URL})
			stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("use a tool")}}}), StreamOptions{ToolChoice: test.give})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			_ = stream.Result()
			if !reflect.DeepEqual(body["tool_choice"], test.want) {
				t.Fatalf("tool_choice = %#v, want %#v", body["tool_choice"], test.want)
			}
		})
	}
}

func TestAnthropicMetadataUserID(t *testing.T) {
	cases := []struct {
		name     string
		metadata map[string]any
		want     any
	}{
		{name: "string", metadata: map[string]any{"user_id": "user-123", "ignored": "value"}, want: map[string]any{"user_id": "user-123"}},
		{name: "non-string", metadata: map[string]any{"user_id": 123}},
		{name: "absent", metadata: map[string]any{"ignored": "value"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer server.Close()

			provider := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "custom", ProviderID: "test-anthropic", BaseURL: server.URL})
			stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{Metadata: test.metadata})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			_ = stream.Result()
			if !reflect.DeepEqual(body["metadata"], test.want) {
				t.Fatalf("metadata = %#v, want %#v", body["metadata"], test.want)
			}
		})
	}
}

func TestAnthropicStreamSessionAffinityAndToolCacheControl(t *testing.T) {
	var gotHeader string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-session-affinity")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	t.Setenv("PI_CACHE_RETENTION", "short")
	sendAffinity := true
	supportsToolCache := false
	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:     "test-key",
		Model:      "claude-sonnet-4-20250514",
		BaseURL:    srv.URL,
		ProviderID: "fireworks",
		Compat: &AnthropicMessagesCompat{
			SendSessionAffinityHeaders:  &sendAffinity,
			SupportsCacheControlOnTools: &supportsToolCache,
		},
	})

	transcript := NormalizeContext(Context{Messages: []Message{
		SystemMessage{ToolsAdded: []ToolSchema{{Name: "bash", Description: "run bash", Parameters: map[string]any{"type": "object"}}}},
		UserMessage{Content: UserText("Hello")},
	}})
	stream, err := p.Stream(context.Background(), transcript, StreamOptions{SessionID: "sess-123"})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	if gotHeader != "sess-123" {
		t.Fatalf("x-session-affinity = %q, want sess-123", gotHeader)
	}
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %#v", body["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("tool = %#v", tools[0])
	}
	if _, ok := tool["cache_control"]; ok {
		t.Fatalf("tool cache_control should be omitted, got %#v", tool["cache_control"])
	}
}

func TestAnthropicStreamCloudflareAffinityAutoDetect(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-session-affinity")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	t.Setenv("PI_CACHE_RETENTION", "short")
	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:     "test-key",
		Model:      "claude-sonnet-4-20250514",
		BaseURL:    srv.URL,
		ProviderID: "cloudflare-ai-gateway",
	})
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{SessionID: "sess-auto"})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	if gotHeader != "" {
		t.Fatalf("x-session-affinity = %q, want empty without anthropic base url hint", gotHeader)
	}
}

func TestAnthropicThinkingConfigClampsThroughThinkingLevelMap(t *testing.T) {
	model := &Model{
		Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh, MaxOutputTokens: 32768},
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingOff:    nil,
			ThinkingMedium: nil,
			ThinkingXHigh:  new("xhigh"),
		},
	}
	// off===null (ThinkingOff: nil, Fable-5 style) → upstream omits the thinking
	// field entirely; must NOT clamp off up to an enabled level (#5567).
	got := thinkingToAnthropicConfig(model, 8192, ThinkingOff)
	if got != nil {
		t.Fatalf("off with thinkingLevelMap.off===null should omit thinking, got %+v", got)
	}
	// Medium is absent from the level map → clamps upward to High.
	// Upstream default budget for High = 16384.
	// adjustedMax = min(8192 + 16384, 32768) = 24576
	// budget (16384) < adjustedMax (24576) → budget stays 16384
	got = thinkingToAnthropicConfig(model, 8192, ThinkingMedium)
	if got == nil || got.Thinking.Type != "enabled" || got.Thinking.BudgetTokens != 16384 {
		t.Fatalf("medium should clamp to high budget=16384, got %+v", got)
	}
	if got.MaxTokens != 24576 {
		t.Fatalf("medium→high: maxTokens should be 24576 (8192+16384), got %d", got.MaxTokens)
	}
	got = thinkingToAnthropicConfig(model, 8192, ThinkingXHigh)
	if got == nil || got.Thinking.BudgetTokens != 16384 {
		t.Fatalf("xhigh should use high budget=16384, got %+v", got)
	}
}

// TestAnthropicThinkingOffVariants pins the #5567 thinking-off contract across
// the thinkingLevelMap.off variants. Upstream (anthropic.ts:982): a reasoning
// model sends thinking:{type:"disabled"} for explicit off UNLESS off===null
// (Fable 5); non-reasoning models omit thinking. Off must never clamp up to an
// enabled level.
func TestAnthropicThinkingOffVariants(t *testing.T) {
	reasoning := func(m ThinkingLevelMap) *Model {
		return &Model{Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh, MaxOutputTokens: 32768}, ThinkingLevelMap: m}
	}
	cases := []struct {
		name     string
		model    *Model
		wantType string // "" means omit (nil result)
	}{
		{"reasoning, off undefined", reasoning(ThinkingLevelMap{ThinkingXHigh: new("xhigh")}), "disabled"},
		{"reasoning, off=string", reasoning(ThinkingLevelMap{ThinkingOff: new("none"), ThinkingXHigh: new("xhigh")}), "disabled"},
		{"reasoning, off=null (Fable 5)", reasoning(ThinkingLevelMap{ThinkingOff: nil, ThinkingXHigh: new("xhigh")}), ""},
		{"non-reasoning", &Model{Capabilities: ModelCapabilities{}}, ""},
		{"nil model", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := thinkingToAnthropicConfig(tc.model, 8192, ThinkingOff)
			if tc.wantType == "" {
				if got != nil {
					t.Fatalf("want omit (nil), got %+v", got)
				}
				return
			}
			if got == nil || got.Thinking == nil || got.Thinking.Type != tc.wantType {
				t.Fatalf("want type %q, got %+v", tc.wantType, got)
			}
			if got.MaxTokens != 0 {
				t.Errorf("disabled must not adjust max_tokens, got %d", got.MaxTokens)
			}
		})
	}
}

// TestAnthropicRefusalPreservesExplanation pins #5666: a refusal stop carries
// stop_details.explanation through to the EventDone ErrorMessage (falling back
// to a generic message when absent), so StopReason=="error" surfaces the reason.
func TestAnthropicRefusalPreservesExplanation(t *testing.T) {
	cases := []struct {
		name       string
		delta      string
		wantErrMsg string
	}{
		{
			"explanation present",
			`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_details":{"type":"refusal","explanation":"I cannot help with that."}},"usage":{"output_tokens":5}}`,
			"I cannot help with that.",
		},
		{
			"no explanation",
			`{"type":"message_delta","delta":{"stop_reason":"refusal"},"usage":{"output_tokens":5}}`,
			"The model refused to complete the request",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
				_, _ = io.WriteString(w, "event: message_delta\ndata: "+tc.delta+"\n\n")
				_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}))
			defer srv.Close()

			p := NewAnthropicProvider(AnthropicConfig{APIKey: "k", Model: "claude-haiku-4-5", BaseURL: srv.URL})
			stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{MaxTokens: 100})
			if err != nil {
				t.Fatal(err)
			}
			result := stream.Result()
			if result.StopReason != StopReasonError || result.ErrorMessage != tc.wantErrMsg {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestAnthropicTemperatureCompatibility(t *testing.T) {
	falseValue := false
	cases := []struct {
		name        string
		model       string
		compat      *AnthropicMessagesCompat
		temperature float64
		set         bool
		want        *float64
	}{
		{name: "omits zero for Claude Opus 4.7", model: "claude-opus-4-7", temperature: 0, set: true},
		{name: "omits zero for Claude Opus 4.8", model: "claude-opus-4-8", temperature: 0, set: true},
		{name: "omits one for Claude Opus 4.7", model: "claude-opus-4-7", temperature: 1, set: true},
		{name: "keeps zero for Claude Opus 4.6", model: "claude-opus-4-6", temperature: 0, set: true, want: new(float64)},
		{name: "keeps zero for Claude Sonnet 4.6", model: "claude-sonnet-4-6", temperature: 0, set: true, want: new(float64)},
		{name: "omits zero when custom model disables temperature", model: "vendor--claude-opus-4-7", compat: &AnthropicMessagesCompat{SupportsTemperature: &falseValue}, temperature: 0, set: true},
		{name: "distinguishes unset zero", model: "claude-opus-4-6", temperature: 0, set: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			provider := &anthropicProvider{cfg: AnthropicConfig{Model: test.model, ProviderID: "anthropic", Compat: test.compat}}
			model := provider.resolveModel()
			params, err := provider.buildParams(model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), false, anthropicHeaders{}, anthropicHeaders{}, StreamOptions{
				Thinking: ThinkingOff, Temperature: test.temperature, TemperatureSet: test.set,
			}, ProviderEnv{"PI_CACHE_RETENTION": "none"})
			if err != nil {
				t.Fatalf("buildParams: %v", err)
			}
			if test.want == nil {
				if params.request.Temperature != nil {
					t.Fatalf("temperature = %v, want omitted", *params.request.Temperature)
				}
				return
			}
			if params.request.Temperature == nil || *params.request.Temperature != *test.want {
				t.Fatalf("temperature = %v, want %v", params.request.Temperature, *test.want)
			}
		})
	}
}

func TestAnthropicAdaptiveThinkingForOpus47(t *testing.T) {
	model := &Model{
		ID:           "claude-opus-4.7",
		Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh},
		ProviderMeta: ProviderMetadata{
			Compat: &OpenAICompat{ForceAdaptiveThinking: new(true)},
		},
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingLevel("xhigh"): new("xhigh"),
		},
	}

	// Adaptive thinking should be used, not budget-based.
	got := thinkingToAnthropicConfig(model, 8192, ThinkingHigh)
	if got == nil {
		t.Fatal("expected non-nil thinking config")
	}
	if got.Thinking.Type != "adaptive" {
		t.Errorf("type = %q, want %q", got.Thinking.Type, "adaptive")
	}
	if got.Thinking.Display != "summarized" {
		t.Errorf("display = %q, want %q", got.Thinking.Display, "summarized")
	}
	if got.Thinking.BudgetTokens != 0 {
		t.Errorf("budget_tokens should be 0 for adaptive, got %d", got.Thinking.BudgetTokens)
	}
	if got.OutputConfig == nil || got.OutputConfig.Effort != "high" {
		t.Errorf("output_config.effort = %v, want %q", got.OutputConfig, "high")
	}

	// xhigh should map via thinkingLevelMap.
	got = thinkingToAnthropicConfig(model, 8192, ThinkingXHigh)
	if got == nil || got.OutputConfig == nil || got.OutputConfig.Effort != "xhigh" {
		t.Errorf("xhigh effort = %v, want 'xhigh'", got)
	}

	// off undefined (no off key) on a reasoning model → upstream sends
	// thinking:{type:"disabled"} (off !== null), even for adaptive models (#5567).
	got = thinkingToAnthropicConfig(model, 8192, ThinkingOff)
	if got == nil || got.Thinking == nil || got.Thinking.Type != "disabled" {
		t.Errorf("off (off undefined) should be thinking:{type:disabled}, got %+v", got)
	}
}

func TestAnthropicBudgetThinkingForOlderModels(t *testing.T) {
	model := &Model{
		ID:           "claude-sonnet-4.5",
		Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh},
	}

	// Older model should use budget-based thinking.
	got := thinkingToAnthropicConfig(model, 8192, ThinkingMedium)
	if got == nil {
		t.Fatal("expected non-nil thinking config")
	}
	if got.Thinking.Type != "enabled" {
		t.Errorf("type = %q, want %q", got.Thinking.Type, "enabled")
	}
	if got.Thinking.Display != "summarized" {
		t.Errorf("display = %q, want %q", got.Thinking.Display, "summarized")
	}
	if got.Thinking.BudgetTokens == 0 {
		t.Error("budget_tokens should be non-zero for budget-based thinking")
	}
	if got.OutputConfig != nil {
		t.Errorf("output_config should be nil for budget-based, got %+v", got.OutputConfig)
	}
}

func TestAnthropicStreamUsesClampedThinkingLevel(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:  "test-key",
		Model:   "claude-opus-4-7",
		BaseURL: srv.URL,
	})
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{
		MaxTokens: 4096, IsReasoning: true, Thinking: ThinkingOff,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	// claude-opus-4-7 has thinkingLevelMap {xhigh} (off undefined) → upstream
	// sends thinking:{type:"disabled"} for an explicit off request (#5567).
	th, ok := body["thinking"].(map[string]any)
	if !ok || th["type"] != "disabled" {
		t.Fatalf("off should send thinking:{type:disabled}, got %#v", body["thinking"])
	}
}

func TestAnthropicStreamUsesModelMaxTokensWhenUnset(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:  "test-key",
		Model:   "claude-sonnet-5",
		BaseURL: srv.URL,
	})
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	if got, _ := body["max_tokens"].(float64); got != 128000 {
		t.Fatalf("max_tokens = %v, want 128000", body["max_tokens"])
	}
}

func TestAnthropicSSE_StreamError(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_err","usage":{"input_tokens":10,"output_tokens":0}}}

event: error
data: {"type":"error","error":{"type":"rate_limit_error","message":"Too many requests"}}

`
	result := anthropicTerminal(t, runAnthropicSSE(t, sse))
	if result.StopReason != StopReasonError || result.ErrorMessage == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestAnthropicSSE_MissingMessageStopIsError(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_partial","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

`
	result := anthropicTerminal(t, runAnthropicSSE(t, sse))
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "Anthropic stream ended before message_stop") {
		t.Fatalf("result = %#v", result)
	}
}

func TestAnthropicSSE_IgnoresUnknownEvents(t *testing.T) {
	sse := `event: message_start
data: {"type":"message_start","message":{"id":"msg_unknown","usage":{"input_tokens":10,"output_tokens":0}}}

event: content_block_delta_types
data: [{"type":"signature_delta","signature":"abc"}]

event: ping
data: {}

event: error
data: {"type":"error","error":{"type":"rate_limit_error","message":"Too many requests"}}

`
	result := anthropicTerminal(t, runAnthropicSSE(t, sse))
	if result.StopReason != StopReasonError {
		t.Fatalf("result = %#v", result)
	}
}

// Anthropic image content must use the "source" base64 shape, and tool-result
// images (e.g. the read tool returning a PNG) must be carried as image blocks
// inside the tool_result content array, not dropped. Regression for the
// read-tool image bug: claude-opus via github-copilot routes through this
// converter, which previously emitted "content" instead of "source" for
// standalone images and silently stripped ToolResultContent.Images.
func TestAnthConvertMessages_ImageContentUsesSourceShape(t *testing.T) {
	out := anthConvertMessages([]Message{UserMessage{Content: UserContentBlocks{
		TextContent{Text: "look"},
		ImageContent{MimeType: "image/png", Data: "AAAA"},
	}}}, false, false, false)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	if !strings.Contains(js, `"source"`) {
		t.Fatalf("standalone image must use \"source\" field: %s", js)
	}
	if strings.Contains(js, `"image","content"`) {
		t.Fatalf("standalone image must not use \"content\" field: %s", js)
	}
	if !strings.Contains(js, `"media_type":"image/png"`) || !strings.Contains(js, `"data":"AAAA"`) {
		t.Fatalf("image source missing media_type/data: %s", js)
	}
}

func TestAnthConvertMessages_ToolResultCarriesImages(t *testing.T) {
	out := anthConvertMessages([]Message{ToolResultMessage{
		ToolCallID: "call_1",
		Content: []ToolResultMessageContent{
			TextContent{Text: "[Image: image/png, 4 bytes]"},
			ImageContent{MimeType: "image/png", Data: "AAAA"},
		},
	}}, false, false, false)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	// Image data must survive into the tool_result content.
	if !strings.Contains(js, `"data":"AAAA"`) {
		t.Fatalf("tool_result image data was dropped: %s", js)
	}
	if !strings.Contains(js, `"type":"image"`) || !strings.Contains(js, `"source"`) {
		t.Fatalf("tool_result image block malformed: %s", js)
	}
	// The placeholder text must still be present alongside the image.
	if !strings.Contains(js, "[Image: image/png, 4 bytes]") {
		t.Fatalf("tool_result text content lost: %s", js)
	}
}

func TestAnthToolResultContent_NoImagesStaysString(t *testing.T) {
	got := anthToolResultContent([]ToolResultMessageContent{TextContent{Text: "plain output"}})
	if s, ok := got.(string); !ok || s != "plain output" {
		t.Fatalf("text-only tool result must stay a string, got %T %v", got, got)
	}
}

func TestAnthToolResultContent_OnlyImagesGetsPlaceholder(t *testing.T) {
	got := anthToolResultContent([]ToolResultMessageContent{ImageContent{MimeType: "image/png", Data: "AAAA"}})
	blocks, ok := got.([]anthContentBlock)
	if !ok || len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (placeholder text + image), got %T %v", got, got)
	}
	if blocks[0].Type != "text" || blocks[0].Text != "(see attached image)" {
		t.Fatalf("missing placeholder text block: %+v", blocks[0])
	}
	if blocks[1].Type != "image" || blocks[1].Source == nil {
		t.Fatalf("missing image block: %+v", blocks[1])
	}
}

// TestAnthropicStreamProviderScopedCacheRetention proves PI_CACHE_RETENTION is
// read from the provider-scoped cfg.Env in preference to the process env: a
// scoped "none" disables the x-session-affinity header even when the process
// env sets "short" (which would otherwise send it). Mirrors upstream
// anthropic.ts resolveCacheRetention(options?.env) / getProviderEnvValue.
func TestAnthropicStreamProviderScopedCacheRetention(t *testing.T) {
	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-session-affinity")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	}))
	defer srv.Close()

	// Process env would send the affinity header; the scoped "none" must win.
	t.Setenv("PI_CACHE_RETENTION", "short")
	sendAffinity := true
	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:     "test-key",
		Model:      "claude-sonnet-4-20250514",
		BaseURL:    srv.URL,
		ProviderID: "fireworks",
		Env:        ProviderEnv{"PI_CACHE_RETENTION": "none"},
		Compat:     &AnthropicMessagesCompat{SendSessionAffinityHeaders: &sendAffinity},
	})

	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{SessionID: "sess-xyz"})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Result()
	if gotHeader != "" {
		t.Fatalf("scoped PI_CACHE_RETENTION=none must omit x-session-affinity, got %q", gotHeader)
	}
}

// TestAnthropicStream_D37_RetriesOnStaleThinkingSignature is the regression
// guard for the D37 thinking-signature recovery. When the provider rejects a
// replayed thinking-block signature (a stale signature the backend no longer
// accepts: observed live on github-copilot claude after rewinds/aging), pig
// must retry once with thinking signatures stripped rather than surfacing a
// hard error that wedges the session. The retry must preserve the reasoning
// text as a plain text block and drop only the signature.
func TestAnthropicStream_D37_RetriesOnStaleThinkingSignature(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"messages.1.content.0: Invalid `+"`signature`"+` in `+"`thinking`"+` block"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"m","usage":{"input_tokens":1,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_stop
data: {"type":"message_stop"}

`)
	}))
	t.Cleanup(srv.Close)

	p := NewAnthropicProvider(AnthropicConfig{
		APIKey:     "test-key",
		Model:      "claude-opus-4.8",
		BaseURL:    srv.URL,
		ProviderID: "github-copilot",
	})

	messages := []Message{
		UserMessage{Content: UserText("hi")},
		AssistantMessage{Provider: "github-copilot", Model: "claude-opus-4.8", Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "let me reason about this", ThinkingSignature: "STALE_SIG_ABC"},
			TextContent{Text: "answer"},
		}},
		UserMessage{Content: UserText("again")},
	}

	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: messages}), StreamOptions{MaxTokens: 128})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if len(result.Content) != 1 || result.Content[0].(TextContent).Text != "ok" {
		t.Fatalf("result = %#v", result)
	}

	if len(bodies) != 2 {
		t.Fatalf("expected exactly 2 requests (fail then retry), got %d", len(bodies))
	}
	// First attempt carries the signed thinking block.
	if !strings.Contains(bodies[0], "STALE_SIG_ABC") {
		t.Fatalf("first request should replay the thinking signature; body:\n%s", bodies[0])
	}
	if !strings.Contains(bodies[0], `"type":"thinking"`) {
		t.Fatalf("first request should contain a thinking block; body:\n%s", bodies[0])
	}
	// Retry must strip the signature and downgrade thinking to text, preserving
	// the reasoning content.
	if strings.Contains(bodies[1], "STALE_SIG_ABC") {
		t.Fatalf("retry must not replay the stale signature; body:\n%s", bodies[1])
	}
	if strings.Contains(bodies[1], `"type":"thinking"`) {
		t.Fatalf("retry must not send a thinking block; body:\n%s", bodies[1])
	}
	if !strings.Contains(bodies[1], "let me reason about this") {
		t.Fatalf("retry must preserve reasoning text as text; body:\n%s", bodies[1])
	}
}

// TestIsThinkingSignatureError checks the D37 error classifier fires only on a
// thinking-block signature error, not on unrelated signature or thinking errors.
func TestIsThinkingSignatureError(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"thinking signature", `{"error":{"type":"invalid_request_error","message":"Invalid ` + "`signature`" + ` in ` + "`thinking`" + ` block"}}`, true},
		{"tool_use unrelated", `{"error":{"type":"invalid_request_error","message":"unexpected ` + "`tool_use_id`" + ` found in ` + "`tool_result`" + ` blocks"}}`, false},
		{"generic signature only", `{"error":{"type":"invalid_request_error","message":"invalid signature header"}}`, false},
		{"not json", `bad gateway`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isThinkingSignatureError([]byte(tc.body)); got != tc.want {
				t.Fatalf("isThinkingSignatureError(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

// TestAnthConvertMessages_ToolResultIsErrorAlwaysEmitted pins that a tool_result
// block always serializes is_error (upstream anthropic-messages.ts:1105 emits
// `is_error: msg.isError` unconditionally), while other block types never carry
// it. pig previously dropped is_error:false via omitempty on a shared struct.
func TestAnthConvertMessages_ToolResultIsErrorAlwaysEmitted(t *testing.T) {
	messages := []Message{ToolResultMessage{
		ToolCallID: "call_1", IsError: false,
		Content: []ToolResultMessageContent{TextContent{Text: "ok"}},
	}}
	out := anthConvertMessages(messages, false, false, false)
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"is_error":false`) {
		t.Errorf("tool_result must emit is_error even when false: %s", raw)
	}
	// A plain text block must not carry is_error.
	textOut := anthConvertMessages([]Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}, false, false, false)
	rawText, _ := json.Marshal(textOut)
	if strings.Contains(string(rawText), "is_error") {
		t.Errorf("non-tool_result blocks must not carry is_error: %s", rawText)
	}
}

// TestApplyConversationCacheControl covers the cache breakpoint on the last user
// message (upstream anthropic-messages.ts:1256-1278): the last block of a final
// user message gets cache_control; a string-content final user message is lifted
// to a text block carrying it; a non-user final message is left untouched.
func TestApplyConversationCacheControl(t *testing.T) {
	cc := &anthCacheControl{Type: "ephemeral"}

	arrayMsgs := []anthMessage{{Role: "user", Content: []anthContentBlock{{Type: "text", Text: "thanks"}}}}
	applyConversationCacheControl(arrayMsgs, cc)
	if blocks := arrayMsgs[0].Content.([]anthContentBlock); blocks[len(blocks)-1].CacheControl != cc {
		t.Errorf("array last-user-block missing cache_control: %#v", blocks)
	}

	stringMsgs := []anthMessage{{Role: "user", Content: "thanks"}}
	applyConversationCacheControl(stringMsgs, cc)
	blocks, ok := stringMsgs[0].Content.([]anthContentBlock)
	if !ok || len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "thanks" || blocks[0].CacheControl != cc {
		t.Errorf("string last-user-message not lifted to cached text block: %#v", stringMsgs[0].Content)
	}

	assistantLast := []anthMessage{{Role: "assistant", Content: []anthContentBlock{{Type: "text", Text: "done"}}}}
	applyConversationCacheControl(assistantLast, cc)
	if blocks := assistantLast[0].Content.([]anthContentBlock); blocks[0].CacheControl != nil {
		t.Errorf("non-user final message must not be cached: %#v", blocks)
	}
}

// TestAnthConvertMessages_ImageAndToolResultImage guards two image paths verified
// byte-identical to pi 0.84.0: a user image lowers to a source base64 block, and a
// tool_result carrying images lowers to a content array [text, image...] (with a
// placeholder text block when the textual result is empty).
func TestAnthConvertMessages_ImageAndToolResultImage(t *testing.T) {
	userImg := anthConvertMessages([]Message{UserMessage{Content: UserContentBlocks{
		ImageContent{MimeType: "image/png", Data: "QUJD"},
	}}}, false, false, false)
	blocks, ok := userImg[0].Content.([]anthContentBlock)
	if !ok || len(blocks) != 1 || blocks[0].Type != "image" {
		t.Fatalf("user image block = %#v", userImg[0].Content)
	}
	src, _ := blocks[0].Source.(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "QUJD" {
		t.Errorf("image source = %#v, want base64/image-png/QUJD", src)
	}

	// tool_result with text + image → [text, image].
	withText := anthToolResultContent([]ToolResultMessageContent{
		TextContent{Text: "captured"}, ImageContent{MimeType: "image/png", Data: "QUJD"},
	})
	tb, ok := withText.([]anthContentBlock)
	if !ok || len(tb) != 2 || tb[0].Type != "text" || tb[0].Text != "captured" || tb[1].Type != "image" {
		t.Errorf("tool_result [text,image] = %#v", withText)
	}
	// image-only tool_result → placeholder text then image.
	imageOnly := anthToolResultContent([]ToolResultMessageContent{ImageContent{MimeType: "image/png", Data: "QUJD"}})
	ib, ok := imageOnly.([]anthContentBlock)
	if !ok || len(ib) != 2 || ib[0].Type != "text" || ib[0].Text != "(see attached image)" || ib[1].Type != "image" {
		t.Errorf("image-only tool_result = %#v, want placeholder text + image", imageOnly)
	}
}

// ─── beta messages endpoint ─────────────────────────────────────────────────

// TestAnthropicSend_UsesBetaMessagesEndpoint is a serialized-request
// regression test: upstream calls client.beta.messages.create
// (anthropic-messages.ts:588, 1065), and the Anthropic SDK's beta namespace
// posts to /v1/messages?beta=true, not /v1/messages
// (@anthropic-ai/sdk/resources/beta/messages/messages.js). Every caller of
// anthropicProvider.send shares this one code path: a direct API key, an
// OAuth subscription token, and the github-copilot Anthropic proxy
// (UseBearerAuth). This test fails on the pre-fix "/v1/messages" URL for
// all three.
func TestAnthropicSend_UsesBetaMessagesEndpoint(t *testing.T) {
	const minimalSSE = "event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	newServer := func(t *testing.T) (*httptest.Server, *string) {
		t.Helper()
		var requestURI string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestURI = r.URL.RequestURI()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, minimalSSE)
		}))
		t.Cleanup(srv.Close)
		return srv, &requestURI
	}

	drain := func(t *testing.T, p Provider) {
		t.Helper()
		transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
		s, err := p.Stream(context.Background(), transcript, StreamOptions{MaxTokens: 16})
		if err != nil {
			t.Fatalf("Stream() error: %v", err)
		}
		for range s.Events(context.Background()) {
		}
	}

	const want = "/v1/messages?beta=true"

	t.Run("api key", func(t *testing.T) {
		srv, requestURI := newServer(t)
		p := NewAnthropicProvider(AnthropicConfig{APIKey: "test-key", Model: "claude-sonnet-4-20250514", BaseURL: srv.URL, ProviderID: "anthropic"})
		drain(t, p)
		if *requestURI != want {
			t.Errorf("request URI = %q, want %q", *requestURI, want)
		}
	})

	t.Run("oauth subscription token", func(t *testing.T) {
		srv, requestURI := newServer(t)
		p := NewAnthropicProvider(AnthropicConfig{APIKey: "sk-ant-oat01-test", Model: "claude-sonnet-4-20250514", BaseURL: srv.URL, ProviderID: "anthropic"})
		drain(t, p)
		if *requestURI != want {
			t.Errorf("request URI = %q, want %q", *requestURI, want)
		}
	})

	t.Run("github-copilot bearer proxy", func(t *testing.T) {
		srv, requestURI := newServer(t)
		p := NewAnthropicProvider(AnthropicConfig{
			Model:         "claude-sonnet-4",
			ProviderID:    "github-copilot",
			UseBearerAuth: true,
			GetAPIKey:     func(context.Context) (string, error) { return "copilot-access-token", nil },
			GetBaseURL:    func(context.Context) (string, error) { return srv.URL, nil },
		})
		drain(t, p)
		if *requestURI != want {
			t.Errorf("request URI = %q, want %q", *requestURI, want)
		}
	})
}
