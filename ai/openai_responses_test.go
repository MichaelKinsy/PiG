package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type responsesTestRoundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTrip responsesTestRoundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func collectResponsesEvents(t *testing.T, sse string) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	provider := &openAIResponsesProvider{}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "model")
	go provider.parseResponsesSSE(context.Background(), strings.NewReader(sse), builder, nil)
	result := builder.stream.Result()
	var events []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		events = append(events, event)
	}
	return result, events
}

func TestOpenAIResponsesSamplingParamsOverrideNamedFields(t *testing.T) {
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{BaseURL: "https://example.test/v1", ProviderID: "custom", Model: "model"}}
	provider.client = &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if payload["temperature"] != 0.25 || payload["top_p"] != 0.9 {
			t.Fatalf("payload = %#v", payload)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
	})}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{Temperature: 0.8, SamplingParams: map[string]any{"temperature": 0.25, "top_p": 0.9}})
	if err != nil {
		t.Fatal(err)
	}
	if stream.Result().StopReason != StopReasonStop {
		t.Fatalf("result = %#v", stream.Result())
	}
}

func TestOpenAIResponsesCanSuppressMaxOutputTokens(t *testing.T) {
	supportsMaxOutputTokens := false
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{BaseURL: "https://example.test/v1", ProviderID: "custom", Model: "model", Compat: &OpenAICompat{SupportsMaxOutputTokens: &supportsMaxOutputTokens}}}
	provider.client = &http.Client{Transport: responsesTestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if _, exists := payload["max_output_tokens"]; exists {
			t.Fatalf("payload contains max_output_tokens: %#v", payload)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
	})}
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{MaxTokens: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_ErrorDoesNotEndIncompleteReasoningItem(t *testing.T) {
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"reason_1","summary":[]}}

data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"partial"}

data: {"type":"error","code":"stream_error","message":"failed"}

`
	_, events := collectResponsesEvents(t, sse)
	for _, event := range events {
		if _, ok := event.(ThinkingEndEvent); ok {
			t.Fatalf("events = %#v, incomplete reasoning item must not emit ThinkingEndEvent", events)
		}
	}
}

func TestResponsesSSE_TextStreaming(t *testing.T) {
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","content":[]}}

data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}

data: {"type":"response.output_text.delta","output_index":0,"delta":" world"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"Hello world"}]}}

data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}

`
	result, _ := collectResponsesEvents(t, sse)
	if result.Content[0].(TextContent).Text != "Hello world" || result.Usage.Input != 10 || result.Usage.Output != 5 {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_ToolCall(t *testing.T) {
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"read","arguments":""}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"main.go\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_abc","name":"read","arguments":"{\"path\":\"main.go\"}"}}

data: {"type":"response.completed","response":{"status":"completed"}}

`
	result, _ := collectResponsesEvents(t, sse)
	tool := result.Content[0].(ToolCall)
	if tool.ID != "call_abc|fc_1" || tool.Arguments["path"] != "main.go" || result.StopReason != StopReasonToolUse {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_ThinkingStream(t *testing.T) {
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"r1"}}

data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"think"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"r1","summary":[{"text":"think"}]}}

data: {"type":"response.completed","response":{"status":"completed"}}

`
	result, _ := collectResponsesEvents(t, sse)
	if result.Content[0].(ThinkingContent).Thinking != "think" {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_ErrorEvent(t *testing.T) {
	result, _ := collectResponsesEvents(t, `data: {"type":"error","code":"bad","message":"failed"}

`)
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "bad") {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_FailedResponse(t *testing.T) {
	result, _ := collectResponsesEvents(t, `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"server","message":"down"}}}

`)
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "server: down") {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_CachedTokens(t *testing.T) {
	result, _ := collectResponsesEvents(t, `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":80}}}}

`)
	if result.Usage.Input != 20 || result.Usage.CacheRead != 80 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestResponsesSSE_CacheWriteTokens(t *testing.T) {
	result, _ := collectResponsesEvents(t, `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":5,"total_tokens":105,"input_tokens_details":{"cached_tokens":20,"cache_write_tokens":30}}}}

`)
	if result.Usage.Input != 50 || result.Usage.CacheRead != 20 || result.Usage.CacheWrite != 30 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestResponsesSSE_RefusalDelta(t *testing.T) {
	sse := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1"}}

data: {"type":"response.refusal.delta","output_index":0,"delta":"I cannot help"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","content":[{"type":"refusal","refusal":"I cannot help"}]}}

data: {"type":"response.completed","response":{"status":"completed"}}

`
	result, _ := collectResponsesEvents(t, sse)
	if result.Content[0].(TextContent).Text != "I cannot help" {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesConvertMessages_UserText(t *testing.T) {
	provider := &openAIResponsesProvider{}
	items, _ := provider.convertMessages([]Message{UserMessage{Content: UserText("hello")}}, nil)
	if len(items) != 1 || items[0].Role != "user" || !strings.Contains(string(items[0].Content), "hello") {
		t.Fatalf("items = %#v", items)
	}
}

func TestResponsesConvertMessages_AssistantToolCall(t *testing.T) {
	provider := &openAIResponsesProvider{}
	items, _ := provider.convertMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{
		TextContent{Text: "Let me check."}, ToolCall{ID: "call_1|fc_1", Name: "read", Arguments: JsonObject{"path": "x.go"}},
	}}}, nil)
	if len(items) != 2 || items[0].Type != "message" || items[1].Type != "function_call" || items[1].CallID != "call_1" || items[1].ID != "fc_1" {
		t.Fatalf("items = %#v", items)
	}
}

func TestResponsesConvertMessages_ToolResult(t *testing.T) {
	provider := &openAIResponsesProvider{}
	items, _ := provider.convertMessages([]Message{ToolResultMessage{ToolCallID: "call_1|fc_1", Content: []ToolResultMessageContent{TextContent{Text: "file contents"}}}}, nil)
	if len(items) != 1 || items[0].Type != "function_call_output" || items[0].CallID != "call_1" {
		t.Fatalf("items = %#v", items)
	}
}

func TestResponsesConvertMessages_ToolRoleMessage(t *testing.T) {
	provider := &openAIResponsesProvider{}
	items, _ := provider.convertMessages([]Message{
		UserMessage{Content: UserText("read foo.go")},
		AssistantMessage{Content: []AssistantContentBlock{ToolCall{ID: "call_abc", Name: "read", Arguments: JsonObject{"path": "foo.go"}}}},
		ToolResultMessage{ToolCallID: "call_abc|fc_1", Content: []ToolResultMessageContent{TextContent{Text: "package foo"}}},
	}, nil)
	for _, item := range items {
		if item.Type == "function_call_output" && item.CallID == "call_abc" {
			return
		}
	}
	t.Fatalf("items = %#v", items)
}

func TestResponsesConvertTools(t *testing.T) {
	provider := &openAIResponsesProvider{}
	tools := []ToolSchema{{Name: "bash", Description: "run bash", Parameters: JsonObject{"type": "object"}}}
	plain, err := provider.convertTools(tools, false, false)
	if err != nil || len(plain) != 1 || plain[0].Strict != nil {
		t.Fatalf("plain = %#v err=%v", plain, err)
	}
	strict, err := provider.convertTools(tools, true, false)
	if err != nil || string(strict[0].Strict) != "false" {
		t.Fatalf("strict = %#v err=%v", strict, err)
	}
}

func TestNewOpenAIResponsesProvider_Defaults(t *testing.T) {
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test", Model: "gpt-4o"})
	if provider.ID() != "openai" || provider.Close() != nil {
		t.Fatalf("provider = %#v", provider)
	}
}

func TestNewOpenAIResponsesProvider_CustomProvider(t *testing.T) {
	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{ProviderID: "github-copilot", Model: "gpt-4o"})
	if provider.ID() != "github-copilot" {
		t.Fatalf("ID = %q", provider.ID())
	}
}

func TestResponsesRequest_ReasoningParams(t *testing.T) {
	model := &Model{Capabilities: ModelCapabilities{MaxThinking: ThinkingHigh}, ThinkingLevelMap: ThinkingLevelMap{ThinkingHigh: new("max")}}
	if thinkingToReasoningEffort(model, ThinkingHigh) != "max" || thinkingToReasoningEffort(model, ThinkingOff) != "" {
		t.Fatal("reasoning effort mapping failed")
	}
}

func TestResponsesReasoningOffDefaultFollowsThinkingLevelMap(t *testing.T) {
	model := &Model{ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: new("disabled")}}
	if *model.ThinkingLevelMap[ThinkingOff] != "disabled" {
		t.Fatal("off mapping lost")
	}
	model.ThinkingLevelMap[ThinkingOff] = nil
	if model.ThinkingLevelMap[ThinkingOff] != nil {
		t.Fatal("explicit nil off mapping lost")
	}
}

func TestResponsesRequest_PromptCaching(t *testing.T) {
	request := respRequest{Model: "gpt-4o", Stream: true, PromptCacheKey: "sess-123", PromptCacheRetention: "24h"}
	encoded, _ := json.Marshal(request)
	if !strings.Contains(string(encoded), `"prompt_cache_key":"sess-123"`) || !strings.Contains(string(encoded), `"prompt_cache_retention":"24h"`) {
		t.Fatalf("request = %s", encoded)
	}
}

func TestResponsesRequest_PromptCachingClampsKey(t *testing.T) {
	key := ClampOpenAIPromptCacheKey(strings.Repeat("🙂", 70))
	if key != strings.Repeat("🙂", openAIPromptCacheKeyMaxLength) {
		t.Fatalf("key = %q", key)
	}
}

func TestResponsesStream_MockServer(t *testing.T) {
	result, _ := collectResponsesEvents(t, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1"}}

data: {"type":"response.output_text.delta","output_index":0,"delta":"hi"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"m1","content":[{"type":"output_text","text":"hi"}]}}

data: {"type":"response.completed","response":{"status":"completed"}}

`)
	if result.Content[0].(TextContent).Text != "hi" {
		t.Fatalf("result = %#v", result)
	}
}

func TestResponsesSSE_ContextCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	builder := newAssistantStreamBuilder(ctx, APIOpenAIResponses, "openai", "model")
	provider := &openAIResponsesProvider{}
	go provider.parseResponsesSSE(ctx, reader, builder, nil)
	_, _ = writer.Write([]byte("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m1\"}}\n\n"))
	cancel()
	_ = writer.Close()
	if builder.stream.Result().StopReason != StopReasonAborted {
		t.Fatalf("result = %#v", builder.stream.Result())
	}
}

// convertResponsesMessages retains empty text even beside a tool call.
func TestResponsesConvertMessages_PreservesEmptyTextBlocks(t *testing.T) {
	provider := &openAIResponsesProvider{}
	items, _ := provider.convertMessages([]Message{
		UserMessage{Content: UserText("hello")},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{}}},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{}, ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{"path": "x.go"}}}},
	}, nil)
	wantTypes := []string{"", "message", "message", "function_call"}
	if len(items) != len(wantTypes) {
		t.Fatalf("items = %#v", items)
	}
	for i, want := range wantTypes {
		if items[i].Type != want {
			t.Errorf("items[%d].Type = %q, want %q", i, items[i].Type, want)
		}
	}
	for _, item := range items[1:3] {
		assertShapeJSON(t, item.Content, `[{"type":"output_text","text":"","annotations":[]}]`)
	}
}

func TestResponsesConvertMessages_ReasoningReplay(t *testing.T) {
	signature := `{"type":"reasoning","id":"rs_123","summary":[{"type":"summary_text","text":"thinking about it"}],"encrypted_content":"abc123"}`
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-5"}}
	items, _ := provider.convertMessages([]Message{AssistantMessage{Provider: "openai", Model: "gpt-5", Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "thinking about it", ThinkingSignature: signature}, TextContent{Text: "answer"},
	}}}, nil)
	if len(items) != 2 || items[0].Type != "reasoning" || items[0].ID != "rs_123" {
		t.Fatalf("items = %#v", items)
	}
}

func TestResponsesConvertMessages_CrossProviderThinkingSkipped(t *testing.T) {
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-5"}}
	items, _ := provider.convertMessages([]Message{AssistantMessage{Provider: "anthropic", Model: "claude", Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "reasoning", ThinkingSignature: "opaque"}, TextContent{Text: "answer"},
	}}}, nil)
	if len(items) != 1 || items[0].Type != "message" {
		t.Fatalf("items = %#v", items)
	}
}
