package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

type openAITestRoundTripperFunc func(*http.Request) (*http.Response, error)

func (f openAITestRoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// ─── Prompt Caching Tests ─────────────────────────────────────────────────────
// Mirrors upstream: packages/ai/test/openai-completions-prompt-cache.test.ts.
// Each test corresponds to a specific upstream test case.

// buildTestRequest simulates the request building path from Stream().
// Exercises the same logic: checks isOpenAIDirectURL + cacheRetention + sessionID.
func buildTestRequest(baseURL, sessionID string) oaiRequest {
	req := oaiRequest{
		Model:    "gpt-4o",
		Messages: []oaiMessage{{Role: "user", Content: "test"}},
		Stream:   true,
	}

	// Mirror the exact logic from Stream()
	cacheRetention := os.Getenv("PI_CACHE_RETENTION")
	if sessionID != "" && isOpenAIDirectURL(baseURL) && cacheRetention != "none" {
		sid := ClampOpenAIPromptCacheKey(sessionID)
		req.PromptCacheKey = &sid
		if cacheRetention == "long" {
			ret := "24h"
			req.PromptCacheRetention = &ret
		}
	}

	return req
}

// TestPromptCache_SetsCacheKeyForOpenAI verifies that prompt_cache_key is
// set to the session ID for direct OpenAI requests when caching is enabled.
// Upstream: "sets prompt_cache_key for direct OpenAI requests when caching is enabled"
func TestPromptCache_SetsCacheKeyForOpenAI(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "") // default = "short"
	req := buildTestRequest("https://api.openai.com/v1", "session-123")
	if req.PromptCacheKey == nil || *req.PromptCacheKey != "session-123" {
		t.Fatalf("prompt_cache_key = %v, want 'session-123'", req.PromptCacheKey)
	}
	if req.PromptCacheRetention != nil {
		t.Fatalf("prompt_cache_retention = %v, want nil (short retention)", *req.PromptCacheRetention)
	}
}

// TestPromptCache_SetsRetention24hForLong verifies that prompt_cache_retention
// is "24h" when PI_CACHE_RETENTION=long.
// Upstream: "sets prompt_cache_retention to 24h for direct OpenAI requests when cacheRetention is long"
func TestPromptCache_SetsRetention24hForLong(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	req := buildTestRequest("https://api.openai.com/v1", "session-456")
	if req.PromptCacheKey == nil || *req.PromptCacheKey != "session-456" {
		t.Fatalf("prompt_cache_key = %v, want 'session-456'", req.PromptCacheKey)
	}
	if req.PromptCacheRetention == nil || *req.PromptCacheRetention != "24h" {
		t.Fatalf("prompt_cache_retention = %v, want '24h'", req.PromptCacheRetention)
	}
}

func TestPromptCache_ClampsCacheKeyForOpenAI(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "")
	sessionID := strings.Repeat("🙂", 70)
	req := buildTestRequest("https://api.openai.com/v1", sessionID)
	if req.PromptCacheKey == nil {
		t.Fatal("prompt_cache_key = nil, want clamped key")
	}
	want := strings.Repeat("🙂", openAIPromptCacheKeyMaxLength)
	if *req.PromptCacheKey != want {
		t.Fatalf("prompt_cache_key = %q, want %q", *req.PromptCacheKey, want)
	}
}

// TestPromptCache_OmitsFieldsWhenNone verifies that both cache fields are
// nil when PI_CACHE_RETENTION=none.
// Upstream: "omits prompt cache fields when cacheRetention is none"
func TestPromptCache_OmitsFieldsWhenNone(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "none")
	req := buildTestRequest("https://api.openai.com/v1", "session-789")
	if req.PromptCacheKey != nil {
		t.Fatalf("prompt_cache_key = %v, want nil", *req.PromptCacheKey)
	}
	if req.PromptCacheRetention != nil {
		t.Fatalf("prompt_cache_retention = %v, want nil", *req.PromptCacheRetention)
	}
}

// TestPromptCache_OmitsFieldsForNonOpenAI verifies that non-OpenAI endpoints
// never receive cache fields regardless of settings.
// Upstream: "omits prompt cache fields for non-OpenAI base URLs"
func TestPromptCache_OmitsFieldsForNonOpenAI(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	for _, baseURL := range []string{
		"https://proxy.example.com/v1",
		"https://api.groq.com/v1",
		"http://localhost:11434/v1",
		"https://api.fireworks.ai/inference/v1",
	} {
		t.Run(baseURL, func(t *testing.T) {
			req := buildTestRequest(baseURL, "session-proxy")
			if req.PromptCacheKey != nil {
				t.Fatalf("prompt_cache_key = %v for %s, want nil", *req.PromptCacheKey, baseURL)
			}
			if req.PromptCacheRetention != nil {
				t.Fatalf("prompt_cache_retention = %v for %s, want nil", *req.PromptCacheRetention, baseURL)
			}
		})
	}
}

// TestPromptCache_EnvVarLongTriggersRetention verifies that PI_CACHE_RETENTION=long
// env var triggers "24h" retention.
// Upstream: "uses PI_CACHE_RETENTION for direct OpenAI requests"
func TestPromptCache_EnvVarLongTriggersRetention(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	req := buildTestRequest("https://api.openai.com/v1", "session-env")
	if req.PromptCacheKey == nil || *req.PromptCacheKey != "session-env" {
		t.Fatalf("prompt_cache_key = %v, want 'session-env'", req.PromptCacheKey)
	}
	if req.PromptCacheRetention == nil || *req.PromptCacheRetention != "24h" {
		t.Fatalf("prompt_cache_retention = %v, want '24h'", req.PromptCacheRetention)
	}
}

// TestPromptCache_OmitsWhenNoSessionID verifies that without a session ID,
// no cache fields are sent regardless of retention setting.
func TestPromptCache_OmitsWhenNoSessionID(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	req := buildTestRequest("https://api.openai.com/v1", "")
	if req.PromptCacheKey != nil {
		t.Fatalf("prompt_cache_key = %v with empty session, want nil", *req.PromptCacheKey)
	}
}

// TestPromptCache_JSONSerialization verifies the actual JSON wire format
// matches what OpenAI expects.
func TestPromptCache_JSONSerialization(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "long")
	req := buildTestRequest("https://api.openai.com/v1", "sess-wire-test")

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}

	// Verify field names match OpenAI API spec exactly
	key, ok := wire["prompt_cache_key"]
	if !ok {
		t.Fatal("JSON missing 'prompt_cache_key'")
	}
	if key != "sess-wire-test" {
		t.Errorf("prompt_cache_key = %v, want 'sess-wire-test'", key)
	}

	ret, ok := wire["prompt_cache_retention"]
	if !ok {
		t.Fatal("JSON missing 'prompt_cache_retention'")
	}
	if ret != "24h" {
		t.Errorf("prompt_cache_retention = %v, want '24h'", ret)
	}
}

// TestPromptCache_JSONOmitsNilFields verifies that nil pointer fields are
// omitted from JSON (not serialized as null).
func TestPromptCache_JSONOmitsNilFields(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "")
	req := buildTestRequest("https://api.groq.com/v1", "some-id")

	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}

	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}

	if _, ok := wire["prompt_cache_key"]; ok {
		t.Error("prompt_cache_key should be omitted for non-OpenAI URL")
	}
	if _, ok := wire["prompt_cache_retention"]; ok {
		t.Error("prompt_cache_retention should be omitted for non-OpenAI URL")
	}
}

// TestIsOpenAIDirectURL verifies the URL detection logic.
func TestIsOpenAIDirectURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://api.openai.com/v1", true},
		{"https://api.openai.com/v1/chat/completions", true},
		{"https://proxy-api.openai.com/v1", true}, // subdomain still matches
		{"https://api.groq.com/v1", false},
		{"http://localhost:11434/v1", false},
		{"https://openrouter.ai/api/v1", false},
		{"https://api.fireworks.ai/inference/v1", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got := isOpenAIDirectURL(tc.url)
			if got != tc.want {
				t.Errorf("isOpenAIDirectURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func openAITestAssistant(blocks ...AssistantContentBlock) []Message {
	return []Message{AssistantMessage{Content: blocks}}
}

// ─── Encrypted Reasoning Tests ────────────────────────────────────────────────
// Mirrors upstream: openai-completions.ts:319-330 (receive) and :793-804 (re-send).

// TestReasoningDetails_ResentOnAssistantMessages verifies that ToolUseContent
// blocks with ThoughtSignature produce reasoning_details on the assistant message
// when converted for sending to the LLM.
func TestReasoningDetails_ResentOnAssistantMessages(t *testing.T) {
	messages := openAITestAssistant(
		TextContent{Text: "Let me check that."},
		ToolCall{ID: "call_abc123", Name: "read", Arguments: JsonObject{"path": "foo.go"}, ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_abc123","data":"enc_data_here"}`},
	)

	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if len(out[0].ReasoningDetails) != 1 {
		t.Fatalf("expected 1 reasoning_details entry, got %d", len(out[0].ReasoningDetails))
	}
	var detail struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(out[0].ReasoningDetails[0], &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Type != "reasoning.encrypted" {
		t.Errorf("detail.type = %q, want 'reasoning.encrypted'", detail.Type)
	}
	if detail.ID != "call_abc123" {
		t.Errorf("detail.id = %q, want 'call_abc123'", detail.ID)
	}
	if detail.Data != "enc_data_here" {
		t.Errorf("detail.data = %q, want 'enc_data_here'", detail.Data)
	}
}

// TestToolResultImage_SeparateUserMessage verifies that a tool result carrying
// an image is lowered into a text-only `tool` message plus a trailing `user`
// message holding the image: because OpenAI Completions tool messages cannot
// carry image parts (the API drops them). Mirrors upstream
// openai-completions.ts:917-983.
func TestToolResultImage_SeparateUserMessage(t *testing.T) {
	messages := []Message{ToolResultMessage{
		ToolCallID: "call_img",
		Content: []ToolResultMessageContent{
			TextContent{Text: "[Image: image/png, 10 bytes]"},
			ImageContent{MimeType: "image/png", Data: "AAAA"},
		},
	}}

	// supportsImages = true: image forwarded as a trailing user turn.
	out, cmErr := convertMessages(messages, true, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (tool + user image), got %d", len(out))
	}
	if out[0].Role != "tool" || out[0].ToolCallID != "call_img" {
		t.Fatalf("first message must be the tool result, got %+v", out[0])
	}
	if s, ok := out[0].Content.(string); !ok || s != "[Image: image/png, 10 bytes]" {
		t.Fatalf("tool message must be text-only, got %#v", out[0].Content)
	}
	if out[1].Role != "user" {
		t.Fatalf("second message must be a user turn, got role %q", out[1].Role)
	}
	parts, ok := out[1].Content.([]oaiContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("user message must hold text + image parts, got %#v", out[1].Content)
	}
	if parts[0].Type != "text" || parts[0].Text != "Attached image(s) from tool result:" {
		t.Fatalf("unexpected lead text part: %#v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil ||
		parts[1].ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Fatalf("unexpected image part: %#v", parts[1])
	}
}

// TestToolResultImageOnly_Placeholder verifies that an image-only tool result
// gets a text placeholder so the tool message is never empty, and that
// supportsImages=false drops the image entirely (no user turn leaks).
func TestToolResultImageOnly_Placeholder(t *testing.T) {
	messages := []Message{ToolResultMessage{
		ToolCallID: "call_img",
		Content:    []ToolResultMessageContent{ImageContent{MimeType: "image/png", Data: "AAAA"}},
	}}

	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 tool message with no image leak, got %d", len(out))
	}
	if s, ok := out[0].Content.(string); !ok || s != "(see attached image)" {
		t.Fatalf("image-only tool result must use placeholder text, got %#v", out[0].Content)
	}
}

// TestReasoningDetails_MultipleToolCalls verifies that multiple tool calls
// with signatures all get included in reasoning_details.
func TestReasoningDetails_MultipleToolCalls(t *testing.T) {
	messages := openAITestAssistant(
		ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "a.go"}, ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_1","data":"data1"}`},
		ToolCall{ID: "call_2", Name: "bash", Arguments: JsonObject{"command": "ls"}, ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_2","data":"data2"}`},
	)

	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if len(out[0].ReasoningDetails) != 2 {
		t.Fatalf("expected 2 reasoning_details entries, got %d", len(out[0].ReasoningDetails))
	}
}

// TestReasoningDetails_OmittedWhenNoSignatures verifies that tool calls
// without ThoughtSignature don't produce reasoning_details.
func TestReasoningDetails_OmittedWhenNoSignatures(t *testing.T) {
	messages := openAITestAssistant(ToolCall{ID: "call_xyz", Name: "bash", Arguments: JsonObject{"command": "ls"}})
	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
	if len(out[0].ReasoningDetails) != 0 {
		t.Errorf("expected no reasoning_details, got %d", len(out[0].ReasoningDetails))
	}
}

// TestReasoningDetails_MixedToolCalls verifies that when some tool calls have
// signatures and others don't, only the ones with signatures contribute.
func TestReasoningDetails_MixedToolCalls(t *testing.T) {
	messages := openAITestAssistant(
		ToolCall{ID: "call_with", Name: "read", Arguments: JsonObject{"path": "x"}, ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_with","data":"encrypted"}`},
		ToolCall{ID: "call_without", Name: "write", Arguments: JsonObject{"path": "y", "content": "z"}},
	)
	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out[0].ReasoningDetails) != 1 {
		t.Fatalf("expected 1 reasoning_details entry, got %d", len(out[0].ReasoningDetails))
	}
	var detail struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out[0].ReasoningDetails[0], &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != "call_with" {
		t.Errorf("reasoning detail ID = %q, want 'call_with'", detail.ID)
	}
}

// TestReasoningDetails_JSONWireFormat verifies the exact JSON structure sent
// to the API matches what OpenAI expects.
func TestReasoningDetails_JSONWireFormat(t *testing.T) {
	messages := openAITestAssistant(
		TextContent{Text: "thinking..."},
		ToolCall{ID: "call_fmt", Name: "read", Arguments: JsonObject{"path": "main.go"}, ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_fmt","data":"base64blob=="}`},
	)

	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	body, err := json.Marshal(out[0])
	if err != nil {
		t.Fatal(err)
	}

	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		t.Fatal(err)
	}

	// Verify reasoning_details is present and is an array
	rd, ok := wire["reasoning_details"]
	if !ok {
		t.Fatal("reasoning_details missing from wire JSON")
	}
	arr, ok := rd.([]any)
	if !ok {
		t.Fatalf("reasoning_details is %T, want []any", rd)
	}
	if len(arr) != 1 {
		t.Fatalf("reasoning_details has %d entries, want 1", len(arr))
	}

	// Verify the detail object structure
	detail, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("detail is %T, want map[string]any", arr[0])
	}
	if detail["type"] != "reasoning.encrypted" {
		t.Errorf("detail.type = %v", detail["type"])
	}
	if detail["id"] != "call_fmt" {
		t.Errorf("detail.id = %v", detail["id"])
	}
	if detail["data"] != "base64blob==" {
		t.Errorf("detail.data = %v", detail["data"])
	}
}

// TestReasoningDetails_PreservedThroughRoundTrip verifies that the
// ThoughtSignature field survives JSON marshal/unmarshal on ToolUseContent.
func TestReasoningDetails_PreservedThroughRoundTrip(t *testing.T) {
	original := ToolCall{
		ID: "call_rt", Name: "bash", Arguments: JsonObject{"command": "echo hi"},
		ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_rt","data":"round_trip_data"}`,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored ToolCall
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.ThoughtSignature != original.ThoughtSignature {
		t.Errorf("ThoughtSignature lost in round-trip: got %q want %q", restored.ThoughtSignature, original.ThoughtSignature)
	}
}

// TestReasoningDetails_NotOnToolResultMessages verifies that reasoning_details
// is only on assistant messages, not on tool result messages.
func TestReasoningDetails_NotOnToolResultMessages(t *testing.T) {
	messages := []Message{
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{
			ID: "call_a", Name: "read", Arguments: JsonObject{"path": "x"},
			ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_a","data":"sig"}`,
		}}},
		ToolResultMessage{ToolCallID: "call_a", Content: []ToolResultMessageContent{TextContent{Text: "file contents here"}}},
	}

	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	// First message (assistant) should have reasoning_details
	if len(out[0].ReasoningDetails) != 1 {
		t.Fatalf("assistant message: expected 1 reasoning_details, got %d", len(out[0].ReasoningDetails))
	}
	// The tool result message should be a separate "tool" role message
	// (OpenAI completions format) with NO reasoning_details
	if len(out) < 2 {
		t.Fatal("expected at least 2 output messages")
	}
	if out[1].Role != "tool" {
		t.Fatalf("second message role = %q, want 'tool'", out[1].Role)
	}
	if len(out[1].ReasoningDetails) != 0 {
		t.Errorf("tool result message should not have reasoning_details, got %d", len(out[1].ReasoningDetails))
	}
}

// TestParseSSEReasoningDetailsEmitOrderedToolCallEvents drives the production
// SSE parser and pins the complete tool-call event contract.
func TestParseSSEReasoningDetailsEmitOrderedToolCallEvents(t *testing.T) {
	ssePayload := `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_sse","type":"function","function":{"name":"read","arguments":""}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"test.go\"}"}}]},"finish_reason":null}]}

data: {"choices":[{"delta":{"reasoning_details":[{"type":"reasoning.encrypted","id":"call_sse","data":"sse_encrypted_payload"}]},"finish_reason":"tool_calls"}]}

data: {"usage":{"prompt_tokens":50,"completion_tokens":10}}

data: [DONE]

`

	provider := &openAIProvider{}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "model")
	provider.parseSSE(context.Background(), strings.NewReader(ssePayload), builder, nil)

	var events []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		events = append(events, event)
	}
	if len(events) != 8 {
		t.Fatalf("events = %#v, want start, tool start, two tool deltas, thinking start, tool end, thinking end, done", events)
	}
	if _, ok := events[0].(StartEvent); !ok {
		t.Fatalf("event[0] = %T, want StartEvent", events[0])
	}
	start, ok := events[1].(ToolCallStartEvent)
	if !ok || start.ContentIndex != 0 {
		t.Fatalf("event[1] = %#v, want ToolCallStartEvent at content index 0", events[1])
	}
	for i, want := range []string{"", `{"path":"test.go"}`} {
		delta, ok := events[i+2].(ToolCallDeltaEvent)
		if !ok || delta.ContentIndex != 0 || delta.Delta != want {
			t.Fatalf("event[%d] = %#v, want ToolCallDeltaEvent index=0 delta=%q", i+2, events[i+2], want)
		}
	}
	thinkingStart, ok := events[4].(ThinkingStartEvent)
	if !ok || thinkingStart.ContentIndex != 1 {
		t.Fatalf("event[4] = %#v, want ThinkingStartEvent at content index 1", events[4])
	}
	end, ok := events[5].(ToolCallEndEvent)
	if !ok || end.ContentIndex != 0 || end.ToolCall.ID != "call_sse" || end.ToolCall.Name != "read" || end.ToolCall.Arguments["path"] != "test.go" || end.ToolCall.ThoughtSignature != "" {
		t.Fatalf("event[5] = %#v, want complete unsigned read tool call", events[5])
	}
	thinkingEnd, ok := events[6].(ThinkingEndEvent)
	if !ok || thinkingEnd.ContentIndex != 1 {
		t.Fatalf("event[6] = %#v, want ThinkingEndEvent at content index 1", events[6])
	}
	done, ok := events[7].(DoneEvent)
	if !ok || done.Reason != StopReasonToolUse || done.Message != builder.stream.Result() {
		t.Fatalf("event[7] = %#v, result=%p", events[7], builder.stream.Result())
	}
	terminalTool := done.Message.Content[0].(ToolCall)
	if terminalTool.ID != end.ToolCall.ID || terminalTool.Name != end.ToolCall.Name || terminalTool.Arguments["path"] != end.ToolCall.Arguments["path"] || terminalTool.ThoughtSignature != "" {
		t.Fatalf("terminal tool call = %#v, end = %#v", terminalTool, end.ToolCall)
	}
	thinking := done.Message.Content[1].(ThinkingContent)
	if !strings.Contains(thinking.ThinkingSignature, `"data":"sse_encrypted_payload"`) {
		t.Fatalf("thinking signature = %q", thinking.ThinkingSignature)
	}
	if done.Message.Usage.Input != 50 || done.Message.Usage.Output != 10 {
		t.Fatalf("terminal usage = %#v", done.Message.Usage)
	}
}

func TestThinkingToReasoningEffort_UsesThinkingLevelMap(t *testing.T) {
	model := &Model{Capabilities: ModelCapabilities{MaxThinking: ThinkingXHigh}, ThinkingLevelMap: ThinkingLevelMap{
		ThinkingMinimal: new("low"),
		ThinkingXHigh:   new("max"),
		ThinkingOff:     new("none"),
	}}
	if got := thinkingToReasoningEffort(model, ThinkingMinimal); got != "low" {
		t.Fatalf("thinkingToReasoningEffort(minimal) = %q, want low", got)
	}
	if got := thinkingToReasoningEffort(model, ThinkingXHigh); got != "max" {
		t.Fatalf("thinkingToReasoningEffort(xhigh) = %q, want max", got)
	}
	if got := thinkingToReasoningEffort(model, ThinkingOff); got != "" {
		t.Fatalf("thinkingToReasoningEffort(off) = %q, want empty", got)
	}
}

func TestDetectCompat_Ollama(t *testing.T) {
	c := DetectCompat("ollama", "http://localhost:11434/v1")
	if c == nil {
		t.Fatal("expected non-nil compat for ollama")
		return
	}
	if *c.SupportsDeveloperRole {
		t.Error("ollama should not support developer role")
	}
	if *c.SupportsReasoningEffort {
		t.Error("ollama should not support reasoning effort")
	}
	if c.MaxTokensField != "max_tokens" {
		t.Errorf("ollama maxTokensField = %q, want max_tokens", c.MaxTokensField)
	}
}

func TestDetectCompat_OpenRouter(t *testing.T) {
	c := DetectCompat("openrouter", "https://openrouter.ai/api/v1")
	if c == nil {
		t.Fatal("expected non-nil compat for openrouter")
		return
	}
	if !*c.SupportsDeveloperRole {
		t.Error("openrouter should support developer role")
	}
	if c.ThinkingFormat != "openrouter" {
		t.Errorf("openrouter thinkingFormat = %q, want openrouter", c.ThinkingFormat)
	}
	if c.MaxTokensField != "max_completion_tokens" {
		t.Errorf("openrouter maxTokensField = %q, want max_completion_tokens", c.MaxTokensField)
	}
}

func TestDetectCompat_DeepSeek(t *testing.T) {
	c := DetectCompat("deepseek", "https://api.deepseek.com/v1")
	if c == nil {
		t.Fatal("expected non-nil compat for deepseek")
		return
	}
	if c.ThinkingFormat != "deepseek" {
		t.Errorf("deepseek thinkingFormat = %q, want deepseek", c.ThinkingFormat)
	}
	if c.RequiresReasoningContentOnAssistantMessages == nil || !*c.RequiresReasoningContentOnAssistantMessages {
		t.Fatal("deepseek should require reasoning_content on assistant messages")
	}
}

func TestDetectCompat_Together(t *testing.T) {
	c := DetectCompat("together", "https://api.together.ai/v1")
	if c == nil {
		t.Fatal("expected non-nil compat for together")
	}
	if c.ThinkingFormat != "together" {
		t.Errorf("together thinkingFormat = %q, want together", c.ThinkingFormat)
	}
	if c.MaxTokensField != "max_tokens" {
		t.Errorf("together maxTokensField = %q, want max_tokens", c.MaxTokensField)
	}
	if c.SupportsReasoningEffort == nil || *c.SupportsReasoningEffort {
		t.Fatal("together should disable reasoning_effort by default")
	}
	if c.SupportsStrictMode == nil || *c.SupportsStrictMode {
		t.Fatal("together should disable strict mode")
	}
	if c.SupportsLongCacheRetention == nil || *c.SupportsLongCacheRetention {
		t.Fatal("together should disable long cache retention")
	}
}

func TestDetectCompat_MoonshotAndCloudflare(t *testing.T) {
	moonshot := DetectCompat("moonshotai", "https://api.moonshot.ai/v1")
	if moonshot == nil {
		t.Fatal("expected non-nil compat for moonshot")
	}
	if moonshot.SupportsReasoningEffort == nil || *moonshot.SupportsReasoningEffort {
		t.Fatal("moonshot should disable reasoning_effort")
	}
	if moonshot.SupportsStrictMode == nil || *moonshot.SupportsStrictMode {
		t.Fatal("moonshot should disable strict mode")
	}

	gateway := DetectCompat("cloudflare-ai-gateway", "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat")
	if gateway == nil {
		t.Fatal("expected non-nil compat for cloudflare ai gateway")
	}
	if gateway.SupportsLongCacheRetention == nil || *gateway.SupportsLongCacheRetention {
		t.Fatal("cloudflare ai gateway should disable long cache retention")
	}
	if gateway.SupportsReasoningEffort == nil || *gateway.SupportsReasoningEffort {
		t.Fatal("cloudflare ai gateway should disable reasoning_effort")
	}
	if gateway.SupportsStrictMode == nil || *gateway.SupportsStrictMode {
		t.Fatal("cloudflare ai gateway should disable strict mode")
	}
}

func TestDetectCompat_OpenAIDirect(t *testing.T) {
	c := DetectCompat("openai", "https://api.openai.com/v1")
	if c != nil {
		t.Fatalf("expected nil compat for OpenAI direct (use defaults), got %+v", c)
	}
}

func TestDetectCompat_GroqHasMaxTokens(t *testing.T) {
	c := DetectCompat("groq", "https://api.groq.com/openai/v1")
	if c == nil {
		t.Fatal("expected non-nil compat for groq")
	}
	if c.MaxTokensField != "max_tokens" {
		t.Errorf("groq maxTokensField = %q, want max_tokens", c.MaxTokensField)
	}
}

func TestSystemPromptRole_NonReasoningModel(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://api.openai.com/v1"}}
	// Non-reasoning model should always get "system" even on OpenAI direct.
	role := p.systemPromptRole(false)
	if role != "system" {
		t.Errorf("non-reasoning model role = %q, want system", role)
	}
}

func TestSystemPromptRole_ReasoningModelOnOpenAI(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://api.openai.com/v1"}}
	role := p.systemPromptRole(true)
	if role != "developer" {
		t.Errorf("reasoning model on OpenAI role = %q, want developer", role)
	}
}

func TestSystemPromptRole_ReasoningModelOnOllama(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "http://localhost:11434/v1"}}
	role := p.systemPromptRole(true)
	if role != "system" {
		t.Errorf("reasoning model on Ollama role = %q, want system (no developer support)", role)
	}
}

func TestConvertMessagesWithCompat_DeepSeekAddsReasoningContent(t *testing.T) {
	tr := true
	p := &openAIProvider{cfg: OpenAIConfig{
		Compat: &OpenAICompat{RequiresReasoningContentOnAssistantMessages: &tr},
	}}
	messages, cmErr := p.convertMessagesWithCompat([]Message{AssistantMessage{Content: []AssistantContentBlock{
		TextContent{Text: "answer"},
	}}}, nil, "system", false)
	if cmErr != nil {
		t.Fatalf("convertMessagesWithCompat: %v", cmErr)
	}
	if len(messages) != 1 || messages[0].ReasoningContent == nil || *messages[0].ReasoningContent != "" {
		t.Fatalf("expected empty reasoning_content on assistant message, got %+v", messages)
	}
}

func TestParseChunkUsage_PromptCacheHitTokens(t *testing.T) {
	u := parseChunkUsage(&oaiUsage{PromptTokens: 100, CompletionTokens: 25, PromptCacheHitTokens: new(60)}, ModelCost{})
	if u.Input != 40 || u.Output != 25 || u.CacheRead != 60 || u.TotalTokens != 125 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

// Upstream parseChunkUsage: prompt_tokens_details.cached_tokens wins over
// prompt_cache_hit_tokens, and cache writes are subtracted from input
// without being subtracted from the cache reads.
func TestParseChunkUsage_PromptTokensDetailsPreferred(t *testing.T) {
	u := parseChunkUsage(&oaiUsage{
		PromptTokens:         100,
		CompletionTokens:     25,
		PromptCacheHitTokens: new(60),
		PromptTokensDetails: &struct {
			CachedTokens     *int `json:"cached_tokens,omitempty"`
			CacheWriteTokens int  `json:"cache_write_tokens,omitempty"`
		}{CachedTokens: new(20), CacheWriteTokens: 7},
		CompletionTokensDetails: &struct {
			ReasoningTokens int `json:"reasoning_tokens,omitempty"`
		}{ReasoningTokens: 9},
	}, ModelCost{})
	if u.Input != 73 || u.CacheRead != 20 || u.CacheWrite != 7 || u.Reasoning == nil || *u.Reasoning != 9 || u.TotalTokens != 125 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

func TestParseSSE_MissingFinishReasonIsError(t *testing.T) {
	message := runOpenAICompletionsSSE(t, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n")
	if message.StopReason != StopReasonError || !strings.Contains(message.ErrorMessage, "finish_reason") {
		t.Fatalf("message = %#v", message)
	}
}

func TestConvertMessagesWithCompat_OpencodeGoRemapsHistoricalReasoning(t *testing.T) {
	provider := &openAIProvider{cfg: OpenAIConfig{ProviderID: "opencode-go"}}
	messages, err := provider.convertMessagesWithCompat([]Message{AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "prior reasoning", ThinkingSignature: "reasoning"},
		TextContent{Text: "answer"},
	}}}, nil, "system", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ReasoningContent == nil || *messages[0].ReasoningContent != "prior reasoning" || messages[0].Reasoning != "" {
		t.Fatalf("messages = %#v, want reasoning_content replay", messages)
	}
}

func TestParseSSE_EncryptedReasoningUsesThinkingSignature(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_sse\",\"type\":\"function\",\"function\":{\"name\":\"read\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_details\":[{\"type\":\"reasoning.encrypted\",\"id\":\"call_sse\",\"data\":\"sse_encrypted_payload\"}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n"
	message := runOpenAICompletionsSSE(t, sse)
	tool := message.Content[0].(ToolCall)
	if tool.ThoughtSignature != "" {
		t.Fatalf("tool thought signature = %q, want empty", tool.ThoughtSignature)
	}
	thinking := message.Content[1].(ThinkingContent)
	var details []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(thinking.ThinkingSignature), &details); err != nil {
		t.Fatalf("unmarshal thinking signature: %v", err)
	}
	if len(details) != 1 || details[0].Type != "reasoning.encrypted" || details[0].ID != "call_sse" || details[0].Data != "sse_encrypted_payload" {
		t.Fatalf("thinking signature = %+v, want encrypted call_sse payload", details)
	}
}

func TestConvertMessagesWithCompat_OpencodeGoReasoningContentOnAssistant(t *testing.T) {
	reasoning := "prior reasoning"
	msg := oaiMessage{Role: "assistant", Content: "answer", ReasoningContent: &reasoning}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(body), `"reasoning_content":"prior reasoning"`) {
		t.Fatalf("json = %s, want reasoning_content field", body)
	}
}

func TestStreamTogetherReasoningPayload(t *testing.T) {
	var reqBody oaiRequest
	p := &openAIProvider{cfg: OpenAIConfig{
		BaseURL:    "https://api.together.ai/v1",
		Model:      "Kimi-K2.6",
		ProviderID: "together",
		Compat: &OpenAICompat{
			ThinkingFormat:             "together",
			SupportsReasoningEffort:    new(false),
			SupportsStrictMode:         new(false),
			SupportsLongCacheRetention: new(false),
			MaxTokensField:             "max_tokens",
		},
	}}
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")),
		}, nil
	})}
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
		IsReasoning: true,
		Thinking:    ThinkingHigh,
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	reasoning, ok := reqBody.Reasoning.(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want map", reqBody.Reasoning)
	}
	if enabled, _ := reasoning["enabled"].(bool); !enabled {
		t.Fatalf("reasoning.enabled = %#v, want true", reasoning["enabled"])
	}
	if reqBody.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want empty when compat disables it", reqBody.ReasoningEffort)
	}
}

func TestStreamTogetherReasoningDisabledPayload(t *testing.T) {
	var reqBody oaiRequest
	p := &openAIProvider{cfg: OpenAIConfig{
		BaseURL:    "https://api.together.ai/v1",
		Model:      "Kimi-K2.6",
		ProviderID: "together",
		Compat: &OpenAICompat{
			ThinkingFormat:          "together",
			SupportsReasoningEffort: new(true),
			MaxTokensField:          "max_tokens",
		},
	}}
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")),
		}, nil
	})}
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
		IsReasoning: true,
		Thinking:    ThinkingOff,
	})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	reasoning, ok := reqBody.Reasoning.(map[string]any)
	if !ok {
		t.Fatalf("reasoning = %#v, want map", reqBody.Reasoning)
	}
	if enabled, _ := reasoning["enabled"].(bool); enabled {
		t.Fatalf("reasoning.enabled = %#v, want false", reasoning["enabled"])
	}
	if reqBody.ReasoningEffort != "" {
		t.Fatalf("reasoning_effort = %q, want empty for off", reqBody.ReasoningEffort)
	}
}

// captureThinkingPayload drives a completions Stream with the given compat +
// thinking level and returns the decoded request body. Oracle for the
// thinkingFormat switch (openai-completions.ts:555-615).
func captureThinkingPayload(t *testing.T, providerID, baseURL string, compat *OpenAICompat, level ThinkingLevel) oaiRequest {
	t.Helper()
	var reqBody oaiRequest
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: baseURL, Model: "m", ProviderID: providerID, Compat: compat}}
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")),
		}, nil
	})}
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{IsReasoning: true, Thinking: level})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	return reqBody
}

func TestStreamZaiThinkingPayload(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "zai", MaxTokensField: "max_tokens"}
	// reasoning on -> thinking:{type:enabled}
	req := captureThinkingPayload(t, "zai", "https://api.z.ai/v1", compat, ThinkingHigh)
	if th, _ := req.Thinking.(map[string]any); th["type"] != "enabled" {
		t.Fatalf("zai thinking = %#v, want {type:enabled}", req.Thinking)
	}
	// reasoning off -> thinking:{type:disabled}
	reqOff := captureThinkingPayload(t, "zai", "https://api.z.ai/v1", compat, ThinkingOff)
	if th, _ := reqOff.Thinking.(map[string]any); th["type"] != "disabled" {
		t.Fatalf("zai thinking(off) = %#v, want {type:disabled}", reqOff.Thinking)
	}
	if reqOff.ReasoningEffort != "" {
		t.Fatalf("zai must not send reasoning_effort, got %q", reqOff.ReasoningEffort)
	}
}

func TestStreamDeepseekThinkingPayload(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "deepseek", SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens"}
	req := captureThinkingPayload(t, "deepseek", "https://api.deepseek.com/v1", compat, ThinkingHigh)
	if th, _ := req.Thinking.(map[string]any); th["type"] != "enabled" {
		t.Fatalf("deepseek thinking = %#v, want {type:enabled}", req.Thinking)
	}
	if req.ReasoningEffort != "high" {
		t.Fatalf("deepseek reasoning_effort = %q, want high", req.ReasoningEffort)
	}
}

func TestStreamQwenThinkingPayload(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "qwen", MaxTokensField: "max_tokens"}
	req := captureThinkingPayload(t, "qwen", "https://example.com/v1", compat, ThinkingHigh)
	if req.EnableThinking == nil || !*req.EnableThinking {
		t.Fatalf("qwen enable_thinking = %v, want true", req.EnableThinking)
	}
	reqOff := captureThinkingPayload(t, "qwen", "https://example.com/v1", compat, ThinkingOff)
	if reqOff.EnableThinking == nil || *reqOff.EnableThinking {
		t.Fatalf("qwen enable_thinking(off) = %v, want false", reqOff.EnableThinking)
	}
}

func TestStreamOpenRouterThinkingPayload(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "openrouter", MaxTokensField: "max_tokens"}
	req := captureThinkingPayload(t, "openrouter", "https://openrouter.ai/api/v1", compat, ThinkingHigh)
	r, ok := req.Reasoning.(map[string]any)
	if !ok || r["effort"] != "high" {
		t.Fatalf("openrouter reasoning = %#v, want {effort:high}", req.Reasoning)
	}
}

func TestConvertMessagesWithCompat_ThinkingAsText(t *testing.T) {
	tr := true
	p := &openAIProvider{cfg: OpenAIConfig{
		Compat: &OpenAICompat{RequiresThinkingAsText: &tr},
	}}
	messages := []Message{
		UserMessage{Content: UserText("think about this")},
		AssistantMessage{Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "Let me reason..."},
			TextContent{Text: "Here's my answer"},
		}},
	}
	out, cmErr := p.convertMessagesWithCompat(messages, nil, "system", false)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	// The assistant message should have thinking converted to text.
	for _, m := range out {
		if m.Role == "assistant" && m.ReasoningDetails != nil {
			t.Fatalf("assistant reasoning details were not stripped: %#v", m.ReasoningDetails)
		}
	}
	// The thinking should appear as text prefix on assistant message.
	var assistantContent string
	for _, m := range out {
		if m.Role == "assistant" {
			if s, ok := m.Content.(string); ok {
				assistantContent = s
			}
		}
	}
	if assistantContent == "" {
		// ThinkingContent becomes reasoning_details in convertMessages, which is then
		// converted to text by requiresThinkingAsText. Check that reasoning_details is nil.
		for _, m := range out {
			if m.Role == "assistant" && len(m.ReasoningDetails) > 0 {
				t.Error("reasoning_details should be cleared when requiresThinkingAsText=true")
			}
		}
	}
}

func TestMaxTokensField_OpenAIDirect(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://api.openai.com/v1"}}
	if f := p.maxTokensField(); f != "max_completion_tokens" {
		t.Errorf("OpenAI direct maxTokensField = %q, want max_completion_tokens", f)
	}
}

func TestMaxTokensField_Ollama(t *testing.T) {
	// Upstream detectCompat (Pi 0.87.1) has no Ollama case, so a local server gets
	// max_completion_tokens unless models.json sets compat.maxTokensField.
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: "http://localhost:11434/v1"}}
	if f := p.maxTokensField(); f != "max_completion_tokens" {
		t.Errorf("Ollama maxTokensField = %q, want max_completion_tokens", f)
	}
	p.cfg.Compat = &OpenAICompat{MaxTokensField: "max_tokens"}
	if f := p.maxTokensField(); f != "max_tokens" {
		t.Errorf("compat override = %q, want max_tokens", f)
	}
}

func TestMaxTokensField_ExplicitOverride(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{
		BaseURL: "https://api.openai.com/v1",
		Compat:  &OpenAICompat{MaxTokensField: "max_tokens"},
	}}
	if f := p.maxTokensField(); f != "max_tokens" {
		t.Errorf("explicit override maxTokensField = %q, want max_tokens", f)
	}
}

func TestCacheControlFormat_Anthropic(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{
		Compat: &OpenAICompat{CacheControlFormat: "anthropic"},
	}}
	if f := p.cacheControlFormat(); f != "anthropic" {
		t.Errorf("cacheControlFormat = %q, want anthropic", f)
	}
}

func TestConvertTools_StrictModeFalse(t *testing.T) {
	f := false
	p := &openAIProvider{cfg: OpenAIConfig{
		Compat: &OpenAICompat{SupportsStrictMode: &f},
	}}
	tools := []ToolSchema{
		{Name: "bash", Description: "run bash", Parameters: map[string]any{"type": "object"}},
	}
	out, err := p.convertTools(tools)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	// openai-completions.ts:1368 omits strict entirely when supportsStrictMode is
	// false; some providers reject the unknown field.
	if out[0].Function.Strict != nil {
		t.Errorf("strict should be omitted when supportsStrictMode=false, got %v", *out[0].Function.Strict)
	}
}

func TestConvertTools_StrictModeDefault(t *testing.T) {
	tools := []ToolSchema{
		{Name: "bash", Description: "run bash", Parameters: map[string]any{"type": "object"}},
	}
	// Upstream detectCompat (Pi 0.87.1) defaults supportsStrictMode to false, so a
	// provider without compat sends no strict field.
	out, err := (&openAIProvider{cfg: OpenAIConfig{}}).convertTools(tools)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	if out[0].Function.Strict != nil {
		t.Fatalf("strict = %v, want the field omitted by default", *out[0].Function.Strict)
	}
	// With strict mode enabled through compat, upstream emits
	// strict: (resolveJsonSchemaStrictSampling ?? false), so a plain tool says false.
	enabled := true
	out, err = (&openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{SupportsStrictMode: &enabled}}}).convertTools(tools)
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	if out[0].Function.Strict == nil || *out[0].Function.Strict {
		t.Fatalf("strict should be false for a plain tool when strict mode is enabled, got %v", out[0].Function.Strict)
	}
}

func TestConvertTools_StrictSchema(t *testing.T) {
	enabled := true
	tool := ToolSchema{
		Name: "lookup", Parameters: map[string]any{
			"type": "object", "properties": map[string]any{
				"value": map[string]any{"type": "string"},
			},
		},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
	}
	out, err := (&openAIProvider{cfg: OpenAIConfig{Compat: &OpenAICompat{SupportsStrictMode: &enabled}}}).convertTools([]ToolSchema{tool})
	if err != nil {
		t.Fatalf("convertTools: %v", err)
	}
	if out[0].Function.Strict == nil || !*out[0].Function.Strict || out[0].Function.Parameters["additionalProperties"] != false {
		t.Fatalf("strict function = %#v", out[0].Function)
	}
	if !slices.Contains(toStringSlice(out[0].Function.Parameters["required"]), "value") {
		t.Fatalf("strict required = %#v", out[0].Function.Parameters["required"])
	}
}

// Pi 0.87.1 (earendil-works/pi#9797) omits empty user text parts and drops a
// user message whose content becomes empty; empty assistant text is skipped.
func TestConvertMessages_DropsEmptyUserBlocksAndSkipsEmptyAssistant(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserText("hello")},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{}}},
		UserMessage{Content: UserContentBlocks{TextContent{}}},
		AssistantMessage{Content: []AssistantContentBlock{
			TextContent{},
			ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "x.go"}},
		}},
	}
	out, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("messages = %#v", out)
	}
	if out[0].Role != "user" || out[0].Content != "hello" {
		t.Fatalf("user text = %#v", out[0])
	}
	if out[1].Role != "assistant" || len(out[1].ToolCalls) != 1 || out[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant tool call = %#v", out[1])
	}
}

func drainParseSSE(t *testing.T, sse string) []AssistantMessageEvent {
	t.Helper()
	provider := &openAIProvider{}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "model")
	provider.parseSSE(context.Background(), strings.NewReader(sse), builder, nil)
	var events []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		events = append(events, event)
	}
	return events
}

func sseTextAndDone(t *testing.T, events []AssistantMessageEvent) (string, *AssistantMessage) {
	t.Helper()
	var text strings.Builder
	var done *AssistantMessage
	for _, event := range events {
		switch event := event.(type) {
		case TextDeltaEvent:
			text.WriteString(event.Delta)
		case DoneEvent:
			done = event.Message
		case ErrorEvent:
			t.Fatalf("unexpected error event: %s", event.Error.ErrorMessage)
		}
	}
	return text.String(), done
}

func TestParseSSEUnknownFinishEmitsError(t *testing.T) {
	cases := []struct {
		name   string
		suffix string
	}{
		{name: "done sentinel", suffix: "\ndata: [DONE]\n"},
		{name: "stream eof"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sse := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"vendor_custom\"}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":2}}\n" + testCase.suffix
			events := drainParseSSE(t, sse)
			if len(events) != 1 {
				t.Fatalf("events = %#v, want one error", events)
			}
			errorEvent, ok := events[0].(ErrorEvent)
			if !ok {
				t.Fatalf("event = %#v, want ErrorEvent", events[0])
			}
			if got := errorEvent.Error.ErrorMessage; got != "Provider finish_reason: vendor_custom" {
				t.Fatalf("error = %q", got)
			}
			if usage := errorEvent.Error.Usage; usage.Input != 7 || usage.Output != 2 || usage.TotalTokens != 9 {
				t.Fatalf("usage = %#v, want input 7 output 2 total 9", usage)
			}
		})
	}
}

func TestParseSSE_UsageInlineWithContentDoesNotAbort(t *testing.T) {
	// gemini-3.5-flash via github-copilot attaches a usage object to
	// content-bearing chunks. The parser must record usage and keep
	// processing; returning on the first usage chunk drops the whole stream
	// (the empty-output bug behind empty advisory reviews).
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"VER\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":1}}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"DICT\"},\"finish_reason\":null}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3}}\n\n" +
		"data: [DONE]\n"

	text, done := sseTextAndDone(t, drainParseSSE(t, sse))
	if text != "VERDICT" {
		t.Fatalf("text = %q, want \"VERDICT\" (content dropped by the usage-abort bug)", text)
	}
	if done == nil {
		t.Fatal("no terminal EventDone emitted")
	}
	if done.StopReason != StopReasonStop {
		t.Errorf("stop reason = %q, want stop", done.StopReason)
	}
	if done.Usage.Output != 3 {
		t.Errorf("usage = %+v, want Output=3 carried to the terminal event", done.Usage)
	}
}

func TestParseSSE_FinishWithUsageNoDoneEmitsTerminal(t *testing.T) {
	// Some providers (e.g. together) close the connection after a final chunk
	// carrying finish_reason + usage, with no [DONE]. The stop reason and
	// usage must still surface on a terminal EventDone via the stream-end path.
	sse := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":4}}\n\n"

	text, done := sseTextAndDone(t, drainParseSSE(t, sse))
	if text != "hi" {
		t.Errorf("text = %q, want \"hi\"", text)
	}
	if done == nil {
		t.Fatal("no terminal EventDone on stream end without [DONE]")
	}
	if done.StopReason != StopReasonStop {
		t.Errorf("stop reason = %q, want stop", done.StopReason)
	}
	if done.Usage.Output != 4 {
		t.Errorf("usage = %+v, want Output=4", done.Usage)
	}
}

// registerTestModel injects a synthetic catalog entry so payload tests can
// control model.ThinkingLevelMap independent of the generated catalog. The
// entry is removed on cleanup. Same-package test seam; not production API.
func registerTestModel(t *testing.T, m GeneratedModel) {
	t.Helper()
	registryOnce.Do(initRegistry)
	gm := new(m)
	fq := m.Provider + "/" + m.ID
	registryByFQ[fq] = gm
	registryByID[m.ID] = append([]*GeneratedModel{gm}, registryByID[m.ID]...)
	t.Cleanup(func() {
		delete(registryByFQ, fq)
		es := registryByID[m.ID]
		for i, e := range es {
			if e == gm {
				registryByID[m.ID] = slices.Delete(es, i, i+1)
				break
			}
		}
		if len(registryByID[m.ID]) == 0 {
			delete(registryByID, m.ID)
		}
	})
}

// captureThinkingPayloadModel is captureThinkingPayload with an explicit model
// id so the catalog ThinkingLevelMap drives the thinkingFormat dispatch.
func captureThinkingPayloadModel(t *testing.T, providerID, baseURL, modelID string, compat *OpenAICompat, level ThinkingLevel) oaiRequest {
	t.Helper()
	var reqBody oaiRequest
	p := &openAIProvider{cfg: OpenAIConfig{BaseURL: baseURL, Model: modelID, ProviderID: providerID, Compat: compat}}
	p.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")),
		}, nil
	})}
	stream, err := p.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{IsReasoning: true, Thinking: level})
	if err != nil {
		t.Fatalf("Stream() error: %v", err)
	}
	for range stream.Events(context.Background()) {
	}
	return reqBody
}

func TestMapOAIFinishReasonCompatibility(t *testing.T) {
	cases := []struct {
		reason  string
		stop    StopReason
		message string
	}{
		{reason: "end", stop: StopReasonStop},
		{reason: "content_filter", stop: StopReasonError, message: "Provider finish_reason: content_filter"},
		{reason: "network_error", stop: StopReasonError, message: "Provider finish_reason: network_error"},
		{reason: "vendor_custom", stop: StopReasonError, message: "Provider finish_reason: vendor_custom"},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			stop, message := mapOAIFinishReason(tc.reason)
			if stop != tc.stop || message != tc.message {
				t.Fatalf("mapOAIFinishReason(%q) = (%q,%q), want (%q,%q)", tc.reason, stop, message, tc.stop, tc.message)
			}
		})
	}
}

func captureOpenAIRequestMap(t *testing.T, providerID, modelID string, compat *OpenAICompat, opts StreamOptions) map[string]any {
	t.Helper()
	var request map[string]any
	provider := &openAIProvider{cfg: OpenAIConfig{
		BaseURL: "https://example.test/v1", Model: modelID, ProviderID: providerID, Compat: compat,
	}}
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")),
		}, nil
	})}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), opts)
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(context.Background()) {
	}
	return request
}

func TestOpenAICompletionsSamplingParamsOverrideNamedFields(t *testing.T) {
	request := captureOpenAIRequestMap(t, "custom", "model", &OpenAICompat{MaxTokensField: "max_tokens"}, StreamOptions{
		Temperature:    0.8,
		SamplingParams: map[string]any{"temperature": 0.2, "top_p": 0.95, "min_p": 0.1},
	})
	if request["temperature"] != 0.2 || request["top_p"] != 0.95 || request["min_p"] != 0.1 {
		t.Fatalf("sampling parameters were not merged last: %v", request)
	}
}

func TestOpenAICompletionsModelSamplingDefaultsMergeBeforeRequestOverrides(t *testing.T) {
	var request map[string]any
	provider := &openAIProvider{cfg: OpenAIConfig{
		BaseURL:        "https://example.test/v1",
		Model:          "model",
		ProviderID:     "custom",
		SamplingParams: map[string]any{"top_p": 0.7, "min_p": 0.1},
	}}
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")),
		}, nil
	})}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{
		SamplingParams: map[string]any{"top_p": 0.9},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(context.Background()) {
	}
	if request["top_p"] != 0.9 || request["min_p"] != 0.1 {
		t.Fatalf("merged sampling parameters = %v", request)
	}
}

func TestOpenAICompletionsCompleteCompatPayload(t *testing.T) {
	priority := 0.0
	registerTestModel(t, GeneratedModel{ID: "compat-model", Provider: "custom", Reasoning: true, MaxOutputTokens: 4096, ThinkingLevelMap: map[ThinkingLevel]*string{ThinkingHigh: new("high")}})
	request := captureOpenAIRequestMap(t, "custom", "compat-model", &OpenAICompat{
		ThinkingFormat: "chat-template", VLLMPriority: &priority,
		ChatTemplateKwargs: map[string]any{"static": true, "enabled": map[string]any{"$var": "thinking.enabled"}, "effort": map[string]any{"$var": "thinking.effort"}, "budget": map[string]any{"$var": "thinking.budget"}},
	}, StreamOptions{IsReasoning: true, Thinking: ThinkingHigh, ThinkingBudgets: &ThinkingBudgets{High: 777}})
	if request["priority"] != float64(0) {
		t.Fatalf("priority = %#v, want 0", request["priority"])
	}
	kwargs, ok := request["chat_template_kwargs"].(map[string]any)
	if !ok || kwargs["static"] != true || kwargs["enabled"] != true || kwargs["effort"] != "high" || kwargs["budget"] != float64(777) {
		t.Fatalf("chat_template_kwargs = %#v", request["chat_template_kwargs"])
	}
}

func TestOpenAICompletionsChatTemplateKwargsOffOmission(t *testing.T) {
	request := captureOpenAIRequestMap(t, "custom", "compat-model", &OpenAICompat{
		ThinkingFormat: "chat-template",
		ChatTemplateKwargs: map[string]any{
			"literal": "kept", "enabled": map[string]any{"$var": "thinking.enabled"},
			"effort": map[string]any{"$var": "thinking.effort", "omitWhenOff": true},
			"budget": map[string]any{"$var": "thinking.budget", "omitWhenOff": true},
		},
	}, StreamOptions{IsReasoning: true, Thinking: ThinkingOff})
	kwargs, ok := request["chat_template_kwargs"].(map[string]any)
	if !ok || kwargs["literal"] != "kept" || kwargs["enabled"] != false {
		t.Fatalf("chat_template_kwargs = %#v", request["chat_template_kwargs"])
	}
	if _, exists := kwargs["effort"]; exists {
		t.Fatalf("off kwargs retained effort: %#v", kwargs)
	}
	if _, exists := kwargs["budget"]; exists {
		t.Fatalf("off kwargs retained budget: %#v", kwargs)
	}
}

func TestOpenAICompletionsCanInferStopWithoutFinishReason(t *testing.T) {
	supportsFinishReason := false
	provider := &openAIProvider{cfg: OpenAIConfig{BaseURL: "https://example.test/v1", ProviderID: "custom", Model: "model", Compat: &OpenAICompat{SupportsFinishReason: &supportsFinishReason}}}
	provider.client = &http.Client{Transport: openAITestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]\n\n"))}, nil
	})}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || result.Content[0].(TextContent).Text != "done" {
		t.Fatalf("result = %#v", result)
	}
}

func TestOpenAICompletionsBasetenAndVLLMThinkingPayload(t *testing.T) {
	registerTestModel(t, GeneratedModel{
		ID: "reasoning-model", Provider: "baseten", Reasoning: true, MaxOutputTokens: 4096,
		ThinkingLevelMap: map[ThinkingLevel]*string{ThinkingHigh: new("high")},
	})
	compat := &OpenAICompat{
		ThinkingFormat:              "baseten",
		SupportsReasoningEffort:     new(true),
		SupportsThinkingTokenBudget: new(true),
		MaxTokensField:              "max_tokens",
		ChatTemplateArgs: map[string]any{
			"enable_thinking": map[string]any{"$var": "thinking.enabled"},
			"effort":          map[string]any{"$var": "thinking.effort"},
		},
	}
	request := captureOpenAIRequestMap(t, "baseten", "reasoning-model", compat, StreamOptions{
		IsReasoning: true,
		Thinking:    ThinkingHigh,
		MaxTokens:   4096,
	})
	args, ok := request["chat_template_args"].(map[string]any)
	if !ok || args["enable_thinking"] != true || args["effort"] != "high" {
		t.Fatalf("Baseten chat_template_args = %#v", request["chat_template_args"])
	}
	if request["reasoning_effort"] != "high" {
		t.Fatalf("Baseten reasoning_effort = %#v, want high", request["reasoning_effort"])
	}
	if request["thinking_token_budget"] != float64(3072) {
		t.Fatalf("vLLM thinking_token_budget = %#v, want 3072", request["thinking_token_budget"])
	}
}

// reasoning is on and compat.supportsReasoningEffort, zai now also sends
// reasoning_effort. With no thinkingLevelMap the raw clamped level is used.
// Mirrors openai-completions.ts zai branch (effort = mapped ?? raw, string only).
func TestStreamZaiReasoningEffortPayload(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "zai", SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens"}
	req := captureThinkingPayload(t, "zai", "https://api.z.ai/v1", compat, ThinkingHigh)
	if th, _ := req.Thinking.(map[string]any); th["type"] != "enabled" {
		t.Fatalf("zai thinking = %#v, want {type:enabled}", req.Thinking)
	}
	if req.ReasoningEffort != "high" {
		t.Fatalf("zai reasoning_effort = %q, want high (raw level)", req.ReasoningEffort)
	}
}

// TestStreamZaiReasoningEffortMapped covers the zai map path: a supported level
// mapped to a string is sent verbatim via the shared clamped mapping.
func TestStreamZaiReasoningEffortMapped(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "zai", SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens"}

	registerTestModel(t, GeneratedModel{ID: "zai-mapped", Provider: "zai", Reasoning: true,
		ThinkingLevelMap: map[ThinkingLevel]*string{ThinkingHigh: new("ultra"), ThinkingXHigh: new("x")}})
	req := captureThinkingPayloadModel(t, "zai", "https://api.z.ai/v1", "zai-mapped", compat, ThinkingHigh)
	if req.ReasoningEffort != "ultra" {
		t.Fatalf("mapped zai reasoning_effort = %q, want ultra", req.ReasoningEffort)
	}
}

// TestStreamDeepseekThinkingOffSuppressed covers the 0.79.5 deepseek change:
// when reasoning is off, thinking:{type:disabled} is suppressed iff
// thinkingLevelMap.off is explicitly null; otherwise it is still sent.
// Mirrors openai-completions.ts deepseek branch (`else if off !== null`).
func TestStreamDeepseekThinkingOffSuppressed(t *testing.T) {
	compat := &OpenAICompat{ThinkingFormat: "deepseek", SupportsReasoningEffort: new(true), MaxTokensField: "max_tokens"}

	registerTestModel(t, GeneratedModel{ID: "ds-offnull", Provider: "deepseek", Reasoning: true,
		ThinkingLevelMap: map[ThinkingLevel]*string{ThinkingOff: nil, ThinkingXHigh: new("x")}})
	reqNull := captureThinkingPayloadModel(t, "deepseek", "https://api.deepseek.com/v1", "ds-offnull", compat, ThinkingOff)
	if reqNull.Thinking != nil {
		t.Fatalf("deepseek thinking(off, off=null) = %#v, want suppressed (nil)", reqNull.Thinking)
	}

	registerTestModel(t, GeneratedModel{ID: "ds-offabsent", Provider: "deepseek", Reasoning: true,
		ThinkingLevelMap: map[ThinkingLevel]*string{ThinkingXHigh: new("x")}})
	reqAbsent := captureThinkingPayloadModel(t, "deepseek", "https://api.deepseek.com/v1", "ds-offabsent", compat, ThinkingOff)
	if th, _ := reqAbsent.Thinking.(map[string]any); th["type"] != "disabled" {
		t.Fatalf("deepseek thinking(off, off absent) = %#v, want {type:disabled}", reqAbsent.Thinking)
	}
}

// TestConvertMessages_AssistantTextIsStringNotArray pins the OpenAI Chat
// Completions request shape for an assistant turn carrying text plus a tool call,
// followed by a tool result. Two upstream-faithful invariants that pig previously
// violated (verified by differential against pi 0.84.0 openai-completions.js):
//
//   - assistant content is a plain string, not a [{type:"text",text}] array
//     (openai-completions.ts:1177-1207: an array makes some models echo the
//     structure literally, e.g. DeepSeek V3.2 via NVIDIA NIM);
//   - request tool_calls carry no "index" field (openai-completions.ts:1188-1210
//     emits only id, type, function; index belongs to streaming deltas).
func TestConvertMessages_AssistantTextIsStringNotArray(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: "look up the weather"}}},
		AssistantMessage{Content: []AssistantContentBlock{
			TextContent{Text: "I'll look it up"},
			ToolCall{ID: "call_1", Name: "lookup", Arguments: JsonObject{"q": "weather"}},
		}},
		ToolResultMessage{ToolCallID: "call_1", Content: []ToolResultMessageContent{TextContent{Text: "sunny and 72F"}}},
	}
	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}

	var assistant *oaiMessage
	for i := range out {
		if out[i].Role == "assistant" {
			assistant = &out[i]
		}
	}
	if assistant == nil {
		t.Fatal("no assistant message produced")
	}
	if got, ok := assistant.Content.(string); !ok || got != "I'll look it up" {
		t.Errorf("assistant content = %#v, want string %q", assistant.Content, "I'll look it up")
	}

	raw, err := json.Marshal(assistant)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"index"`) {
		t.Errorf("request tool_calls must not carry an index field: %s", raw)
	}
	if !strings.Contains(string(raw), `"content":"I'll look it up"`) {
		t.Errorf("assistant content must serialize as a string, got: %s", raw)
	}

	// The tool result must lower to a role:tool message keyed by tool_call_id.
	var toolMsg *oaiMessage
	for i := range out {
		if out[i].Role == "tool" {
			toolMsg = &out[i]
		}
	}
	if toolMsg == nil || toolMsg.ToolCallID != "call_1" || toolMsg.Content != "sunny and 72F" {
		t.Errorf("tool result message = %#v, want role:tool content=%q tool_call_id=call_1", toolMsg, "sunny and 72F")
	}
}

// TestConvertMessages_UserImageIsImageURLDataURL guards the multi-part user
// content path verified byte-identical to pi 0.84.0: an image block lowers to an
// image_url part carrying a data: URL, alongside the text part, and the user
// content stays an array (only assistant content collapses to a string).
func TestConvertMessages_UserImageIsImageURLDataURL(t *testing.T) {
	messages := []Message{UserMessage{Content: UserContentBlocks{
		TextContent{Text: "what is this?"},
		ImageContent{MimeType: "image/png", Data: "QUJD"},
	}}}
	out, cmErr := convertMessages(messages, false, nil)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}
	if len(out) != 1 || out[0].Role != "user" {
		t.Fatalf("expected one user message, got %#v", out)
	}
	parts, ok := out[0].Content.([]oaiContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("user content must stay a 2-part array, got %#v", out[0].Content)
	}
	if parts[0].Type != "text" || parts[0].Text != "what is this?" {
		t.Errorf("part[0] = %#v, want text", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || parts[1].ImageURL.URL != "data:image/png;base64,QUJD" {
		t.Errorf("part[1] = %#v, want image_url data URL", parts[1])
	}
}

// TestOpenAICompletionsEmptyTools ports upstream empty-tools handling:
// packages/ai/test/openai-completions-empty-tools.test.ts. The request `tools`
// field is omitted when there are no active tools and no tool history, but must
// be an explicit empty array once the conversation carries tool history so that
// Anthropic-via-proxy (LiteLLM) backends accept the request.
func TestOpenAICompletionsEmptyTools(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{Model: "gpt-4o-mini", BaseURL: "https://api.openai.com/v1"}}
	user := UserMessage{Content: UserText("hi")}
	toolHistory := []Message{
		user,
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "t1", Name: "noop", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "t1", Content: []ToolResultMessageContent{TextContent{Text: "done"}}},
	}
	oneTool := []ToolSchema{{Name: "noop", Description: "d", Parameters: map[string]any{"type": "object"}}}

	cases := []struct {
		name      string
		messages  []Message
		tools     []ToolSchema
		wantKey   bool
		wantEmpty bool
	}{
		{"empty tools no history omits", []Message{user}, []ToolSchema{}, false, false},
		{"nil tools no history omits", []Message{user}, nil, false, false},
		{"empty tools with history emits empty array", toolHistory, []ToolSchema{}, true, true},
		{"active tools emit populated array", []Message{user}, oneTool, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := oaiRequest{Model: p.cfg.Model, Messages: []oaiMessage{{Role: "user", Content: "hi"}}, Stream: true}
			tools, terr := p.resolveRequestTools(tc.messages, tc.tools)
			if terr != nil {
				t.Fatalf("resolveRequestTools: %v", terr)
			}
			req.Tools = tools
			body, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatal(err)
			}
			raw, present := wire["tools"]
			if present != tc.wantKey {
				t.Fatalf("tools present = %v, want %v (body=%s)", present, tc.wantKey, body)
			}
			if !present {
				return
			}
			if tc.wantEmpty && string(raw) != "[]" {
				t.Fatalf("tools = %s, want []", raw)
			}
			if !tc.wantEmpty && string(raw) == "[]" {
				t.Fatalf("tools = [], want a populated array")
			}
		})
	}
}

// TestNormalizeCompletionsToolCallID ports the hermetic contract behind upstream
// packages/ai/test/tool-call-id-normalization.test.ts (a live cross-provider
// suite): OpenAI-Responses pipe-format ids ({call_id}|{item_id}, up to 400+ chars)
// must be normalized to Chat Completions safe ids so proxies do not reject them
// as "call_id too long". Golden values are produced by upstream's own
// normalizeToolCallId + utils/hash.ts shortHash.
func TestNormalizeCompletionsToolCallID(t *testing.T) {
	// The exact failing id from issue #1022.
	fail := "call_pAYbIr76hXIjncD9UE4eGfnS|t5nnb2qYMFWGSsr13fhCd1CaCu3t3qONEPuOudu4HSVEtA8YJSL6FAZUxvoOoD792VIJWl91g87EdqsCWp9krVsdBysQoDaf9lMCLb8BS4EYi4gQd5kBQBYLlgD71PYwvf+TbMD9J9/5OMD42oxSRj8H+vRf78/l2Xla33LWz4nOgsddBlbvabICRs8GHt5C9PK5keFtzyi3lsyVKNlfduK3iphsZqs4MLv4zyGJnvZo/+QzShyk5xnMSQX/f98+aEoNflEApCdEOXipipgeiNWnpFSHbcwmMkZoJhURNu+JEz3xCh1mrXeYoN5o+trLL3IXJacSsLYXDrYTipZZbJFRPAucgbnjYBC+/ZzJOfkwCs+Gkw7EoZR7ZQgJ8ma+9586n4tT4cI8DEhBSZsWMjrCt8dxKg=="
	cases := []struct {
		name, id, provider, want string
	}{
		{"short pipe combines parts", "call_abc|item_xyz", "github-copilot", "call_abc_item_xyz"},
		{"long pipe falls back to hash", fail, "github-copilot", "call_pAYbIr76hXIjncD9UE4eGfnS_1q6evuz1"},
		{"special chars sanitized", "call_a+b/c|d=e/f", "github-copilot", "call_a_b_c_d_e_f"},
		{"openai truncates long non-pipe id", strings.Repeat("x", 45), "openai", strings.Repeat("x", 40)},
		{"non-openai passes long id through", strings.Repeat("y", 50), "anthropic", strings.Repeat("y", 50)},
		{"short id unchanged", "call_123", "openai", "call_123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCompletionsToolCallID(tc.id, tc.provider); got != tc.want {
				t.Fatalf("normalizeCompletionsToolCallID(%.24q, %q) = %q, want %q", tc.id, tc.provider, got, tc.want)
			}
			if got := normalizeCompletionsToolCallID(tc.id, tc.provider); len(got) > 40 && strings.Contains(tc.id, "|") {
				t.Fatalf("pipe id normalized to %d chars, exceeds 40-char limit", len(got))
			}
		})
	}
}

// TestConvertMessagesNormalizesToolCallIDsConsistently drives the production
// convertMessagesWithCompat path and proves an assistant tool call and its
// matching tool result keep the same normalized id, so the request stays valid.
func TestConvertMessagesNormalizesToolCallIDsConsistently(t *testing.T) {
	p := &openAIProvider{cfg: OpenAIConfig{ProviderID: "github-copilot", Model: "gpt-5.2-codex"}}
	pipeID := "call_abc|item_xyz"
	messages := []Message{
		UserMessage{Content: UserText("hi")},
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: pipeID, Name: "echo", Arguments: JsonObject{"message": "hi"}}}},
		ToolResultMessage{ToolCallID: pipeID, Content: []ToolResultMessageContent{TextContent{Text: "hi"}}},
	}
	out, cmErr := p.convertMessagesWithCompat(messages, nil, "system", false)
	if cmErr != nil {
		t.Fatalf("convertMessages: %v", cmErr)
	}

	var callID, resultID string
	for _, m := range out {
		for _, tc := range m.ToolCalls {
			callID = tc.ID
		}
		if m.ToolCallID != "" {
			resultID = m.ToolCallID
		}
	}
	if callID == "" || resultID == "" {
		t.Fatalf("missing tool call/result id: call=%q result=%q", callID, resultID)
	}
	if callID != "call_abc_item_xyz" {
		t.Fatalf("tool call id = %q, want normalized call_abc_item_xyz", callID)
	}
	if callID != resultID {
		t.Fatalf("tool call id %q and tool result id %q diverged; provider would reject the request", callID, resultID)
	}
	if strings.Contains(callID, "|") {
		t.Fatalf("normalized id still contains a pipe: %q", callID)
	}
}
