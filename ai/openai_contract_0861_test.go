package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestOpenAICompletionsRequestHeadersOverrideConfiguredHeaders(t *testing.T) {
	requestHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestHeaders <- request.Header.Clone()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	provider := NewOpenAIProvider(OpenAIConfig{
		BaseURL:    server.URL,
		Model:      "test",
		ProviderID: "openrouter",
		ExtraHeaders: map[string]string{
			"X-OpenRouter-Title": "configured-title",
			"X-Configured":       "configured-value",
		},
	})
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: "hello"}}},
	}}), StreamOptions{Headers: ProviderHeadersFromStrings(map[string]string{
		"X-OpenRouter-Title": "request-title",
		"X-Request":          "request-value",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result == nil || result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}

	headers := <-requestHeaders
	if got := headers.Get("X-OpenRouter-Title"); got != "request-title" {
		t.Fatalf("X-OpenRouter-Title = %q, want request-title", got)
	}
	if got := headers.Get("X-Configured"); got != "configured-value" {
		t.Fatalf("X-Configured = %q, want configured-value", got)
	}
	if got := headers.Get("X-Request"); got != "request-value" {
		t.Fatalf("X-Request = %q, want request-value", got)
	}
}

func TestOpenAICompletionsNativeThinkingDoesNotEnterAssistantText(t *testing.T) {
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "private reasoning", ThinkingSignature: "reasoning_content"},
		TextContent{Text: "visible answer"},
	}}}
	converted, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) != 1 {
		t.Fatalf("converted messages = %#v", converted)
	}
	if converted[0].Content != "visible answer" {
		t.Fatalf("assistant content = %#v, want visible answer", converted[0].Content)
	}
	if converted[0].ReasoningContent == nil || *converted[0].ReasoningContent != "private reasoning" {
		t.Fatalf("reasoning_content = %#v, want private reasoning", converted[0].ReasoningContent)
	}
}

func TestOpenAICompletionsThinkingAsTextUsesPlainContentParts(t *testing.T) {
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "private reasoning"},
		TextContent{Text: "visible answer"},
	}}}
	converted, err := convertMessagesInternal(messages, false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) != 1 {
		t.Fatalf("converted messages = %#v", converted)
	}
	parts, ok := converted[0].Content.([]oaiContentPart)
	if !ok || len(parts) != 2 {
		t.Fatalf("thinking-as-text content = %#v, want two content parts", converted[0].Content)
	}
	if parts[0].Text != "private reasoning" || parts[1].Text != "visible answer" {
		t.Fatalf("thinking-as-text parts = %#v", parts)
	}
}

func TestOpenAICompletionsPreservesStructuredReasoningDetails(t *testing.T) {
	details := []map[string]any{{"type": "reasoning.encrypted", "id": "call", "data": "opaque"}}
	signature, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{ThinkingSignature: string(signature)},
		ToolCall{ID: "call", Name: "read", Arguments: JsonObject{}},
	}}}
	converted, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) != 1 || len(converted[0].ReasoningDetails) != 1 {
		t.Fatalf("reasoning details = %#v", converted)
	}
	var got map[string]any
	if err := json.Unmarshal(converted[0].ReasoningDetails[0], &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, details[0]) {
		t.Fatalf("reasoning detail = %#v, want %#v", got, details[0])
	}
}

func TestOpenAICompletionsBatchesToolResultImagesAfterNewlineJoinedText(t *testing.T) {
	messages := []Message{
		ToolResultMessage{ToolCallID: "one", ToolName: "first", Content: []ToolResultMessageContent{
			TextContent{Text: "line one"}, TextContent{Text: "line two"}, ImageContent{MimeType: "image/png", Data: "AAAA"},
		}},
		ToolResultMessage{ToolCallID: "two", ToolName: "second", Content: []ToolResultMessageContent{
			ImageContent{MimeType: "image/png", Data: "BBBB"},
		}},
	}
	converted, err := convertMessages(messages, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) != 3 {
		t.Fatalf("converted count = %d, want two tool results and one image turn: %#v", len(converted), converted)
	}
	if converted[0].Role != "tool" || converted[0].Content != "line one\nline two" {
		t.Fatalf("first tool result = %#v", converted[0])
	}
	if converted[1].Role != "tool" || converted[1].Content != "(see attached image)" {
		t.Fatalf("second tool result = %#v", converted[1])
	}
	if converted[2].Role != "user" {
		t.Fatalf("image turn role = %q, want user", converted[2].Role)
	}
	parts, ok := converted[2].Content.([]oaiContentPart)
	if !ok || len(parts) != 3 {
		t.Fatalf("image turn = %#v, want label and two images", converted[2].Content)
	}
}

func TestOpenAICompletionsImageOnlyResultKeepsPlaceholderForTextModel(t *testing.T) {
	messages := []Message{ToolResultMessage{ToolCallID: "one", ToolName: "read", Content: []ToolResultMessageContent{
		ImageContent{MimeType: "image/png", Data: "AAAA"},
	}}}
	converted, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(converted) != 1 || converted[0].Content != "(see attached image)" {
		t.Fatalf("image-only text-model result = %#v", converted)
	}
}
