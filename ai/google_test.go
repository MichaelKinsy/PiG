package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runGoogleSSE(t *testing.T, sseData string) []AssistantMessageEvent {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !strings.Contains(request.URL.Path, "streamGenerateContent") {
			t.Errorf("unexpected path: %s", request.URL.Path)
		}
		if !strings.Contains(request.URL.RawQuery, "alt=sse") {
			t.Errorf("missing alt=sse in query: %s", request.URL.RawQuery)
		}
		if got := request.Header.Get("x-goog-api-key"); got != "test-key" {
			t.Errorf("x-goog-api-key = %q, want test-key", got)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, sseData)
	}))
	t.Cleanup(server.Close)

	provider := NewGoogleProvider(GoogleConfig{
		APIKey: "test-key", Model: "gemini-2.5-flash", BaseURL: server.URL, ProviderID: "google-generative-ai",
	})
	transcript := NormalizeContext(Context{Messages: []Message{
		SystemMessage{Content: SystemText("You are helpful")},
		UserMessage{Content: UserText("Hello")},
	}})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{MaxTokens: 1024})
	if err != nil {
		t.Fatal(err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(context.Background()) {
		events = append(events, event)
	}
	return events
}

func TestGoogleSSE_TextOnly(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":" world!"}]}}]}

data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

`
	events := runGoogleSSE(t, sse)
	var text strings.Builder
	var final *AssistantMessage
	for _, event := range events {
		switch event := event.(type) {
		case TextDeltaEvent:
			text.WriteString(event.Delta)
		case DoneEvent:
			final = event.Message
		}
	}
	if text.String() != "Hello world!" || final == nil {
		t.Fatalf("text=%q final=%#v", text.String(), final)
	}
	if final.Usage.Input != 10 || final.Usage.Output != 5 || final.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %#v", final.Usage)
	}
}

func TestGoogleSSE_ToolCall(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Let me check."}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"bash","args":{"command":"ls -la"}}}]}}]}

data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":20,"candidatesTokenCount":10,"totalTokenCount":30}}

`
	message := googleTerminalMessage(t, runGoogleSSE(t, sse))
	if message.StopReason != StopReasonToolUse || len(message.Content) != 2 {
		t.Fatalf("message = %#v", message)
	}
	tool, ok := message.Content[1].(ToolCall)
	if !ok || tool.Name != "bash" || tool.Arguments["command"] != "ls -la" {
		t.Fatalf("tool = %#v", message.Content[1])
	}
}

func TestGoogleSSE_Thinking(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Let me think...","thought":true}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Here is the answer."}]}}]}

data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":8,"totalTokenCount":18,"thoughtsTokenCount":5}}

`
	message := googleTerminalMessage(t, runGoogleSSE(t, sse))
	if len(message.Content) != 2 {
		t.Fatalf("content = %#v", message.Content)
	}
	thinking := message.Content[0].(ThinkingContent)
	text := message.Content[1].(TextContent)
	if thinking.Thinking != "Let me think..." || text.Text != "Here is the answer." {
		t.Fatalf("content = %#v", message.Content)
	}
	if message.Usage.Output != 13 || message.Usage.Reasoning == nil || *message.Usage.Reasoning != 5 {
		t.Fatalf("usage = %#v", message.Usage)
	}
}

func TestGoogleSSE_CachedTokens(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}

data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":2,"totalTokenCount":102,"cachedContentTokenCount":80}}

`
	usage := googleTerminalMessage(t, runGoogleSSE(t, sse)).Usage
	if usage.Input != 20 || usage.CacheRead != 80 {
		t.Fatalf("usage = %#v", usage)
	}
}

func TestGoogleSSE_ErrorRetainsUsage(t *testing.T) {
	sse := `data: {"candidates":[{"finishReason":"SAFETY"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":2,"totalTokenCount":9}}

`
	message := googleTerminalMessage(t, runGoogleSSE(t, sse))
	if message.StopReason != StopReasonError || message.ErrorMessage != "Provider stopped with: SAFETY" {
		t.Fatalf("message = %#v", message)
	}
	if message.Usage.Input != 7 || message.Usage.Output != 2 || message.Usage.TotalTokens != 9 {
		t.Fatalf("usage = %#v, want input 7 output 2 total 9", message.Usage)
	}
}

func TestGoogleStrictToolSchemaWire(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
	}))
	t.Cleanup(server.Close)

	provider := NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "gemini-3-flash", BaseURL: server.URL, ProviderID: "google-generative-ai"})
	transcript := NormalizeContext(Context{Messages: []Message{
		SystemMessage{ToolsAdded: []ToolSchema{{
			Name: "lookup", Description: "lookup",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"required_value": map[string]any{"type": "string"},
				"optional_value": map[string]any{"type": "number"},
			}, "required": []any{"required_value"}},
			ConstrainedSampling: &ConstrainedSamplingConfig{Type: "json_schema", Strict: "prefer"},
		}}},
		UserMessage{Content: UserText("call it")},
	}})
	stream, err := provider.Stream(context.Background(), transcript, StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = stream.Result()

	tools := body["tools"].([]any)
	declarations := tools[0].(map[string]any)["functionDeclarations"].([]any)
	schema := declarations[0].(map[string]any)["parametersJsonSchema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatalf("parametersJsonSchema = %#v, want strict conversion", schema)
	}
	mode := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)["mode"]
	if mode != "VALIDATED" {
		t.Fatalf("function calling mode = %#v, want VALIDATED", mode)
	}
}

func TestGoogleSSE_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":{"code":400,"message":"Invalid API key","status":"INVALID_ARGUMENT"}}`)
	}))
	t.Cleanup(server.Close)
	provider := NewGoogleProvider(GoogleConfig{APIKey: "bad-key", Model: "gemini-2.5-flash", BaseURL: server.URL})
	_, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}), StreamOptions{MaxTokens: 1024})
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("error = %v", err)
	}
}

func TestGoogleSSE_EmptyStream(t *testing.T) {
	message := googleTerminalMessage(t, runGoogleSSE(t, ""))
	if message.StopReason != StopReasonError || message.ErrorMessage == "" {
		t.Fatalf("message = %#v", message)
	}
}

func TestGoogleConvertMessages(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserText("Hello")},
		AssistantMessage{Provider: "google", Model: "gemini-2.5-flash", Content: []AssistantContentBlock{
			TextContent{Text: "Hi there"}, ToolCall{ID: "t1", Name: "bash", Arguments: JsonObject{"command": "ls"}},
		}},
		ToolResultMessage{ToolCallID: "t1", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "file.txt"}}},
	}
	output := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	if len(output) != 3 || output[0].Role != "user" || output[1].Role != "model" || output[2].Role != "user" || output[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("output = %#v", output)
	}
}

func TestGoogleConvertMessages_ToolResultMerge(t *testing.T) {
	messages := []Message{
		ToolResultMessage{ToolCallID: "t1", ToolName: "one", Content: []ToolResultMessageContent{TextContent{Text: "result1"}}},
		ToolResultMessage{ToolCallID: "t2", ToolName: "two", Content: []ToolResultMessageContent{TextContent{Text: "result2"}}},
	}
	output := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	if len(output) != 1 || len(output[0].Parts) != 2 {
		t.Fatalf("output = %#v", output)
	}
}

func TestGoogleConvertMessages_ErrorResult(t *testing.T) {
	messages := []Message{ToolResultMessage{
		ToolCallID: "t1", ToolName: "read", IsError: true,
		Content: []ToolResultMessageContent{TextContent{Text: "not found"}},
	}}
	output := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	response := output[0].Parts[0].FunctionResponse
	if response == nil || response.Response["error"] == nil {
		t.Fatalf("response = %#v", response)
	}
}

func TestGeminiThinkingBudget(t *testing.T) {
	tests := []struct {
		level ThinkingLevel
		model string
		want  int
	}{
		{ThinkingMinimal, "gemini-2.5-pro", 128},
		{ThinkingHigh, "gemini-2.5-pro", 32768},
		{ThinkingLow, "gemini-2.5-flash", 2048},
		{ThinkingMedium, "gemini-2.5-flash-lite", 8192},
		{ThinkingHigh, "gemini-1.5-pro", -1},
	}
	for _, test := range tests {
		if got := geminiThinkingBudget(test.level, test.model); got != test.want {
			t.Errorf("budget %q/%q = %d, want %d", test.level, test.model, got, test.want)
		}
	}
}

func TestGeminiThinkingLevel(t *testing.T) {
	tests := []struct {
		level ThinkingLevel
		model string
		want  string
	}{
		{ThinkingMinimal, "gemini-3-pro", "LOW"},
		{ThinkingHigh, "gemini-3-pro", "HIGH"},
		{ThinkingLow, "gemma-4", "MINIMAL"},
		{ThinkingMedium, "gemini-3-flash", "MEDIUM"},
	}
	for _, test := range tests {
		if got := geminiThinkingLevel(test.level, test.model); got != test.want {
			t.Errorf("level %q/%q = %q, want %q", test.level, test.model, got, test.want)
		}
	}
}

func TestGeminiModelDetection(t *testing.T) {
	tests := []struct {
		model             string
		pro, flash, gemma bool
	}{
		{"gemini-3-pro", true, false, false},
		{"gemini-3.1-pro", true, false, false},
		{"gemini-3-flash", false, true, false},
		{"gemma-4", false, false, true},
		{"gemma4", false, false, true},
		{"gemini-2.5-flash", false, false, false},
	}
	for _, test := range tests {
		if isGemini3Pro(test.model) != test.pro || isGemini3Flash(test.model) != test.flash || isGemma4(test.model) != test.gemma {
			t.Errorf("model detection failed for %q", test.model)
		}
	}
}

func TestGoogleConvertMessages_FunctionResponseCarriesToolName(t *testing.T) {
	messages := []Message{
		AssistantMessage{Provider: "google", Model: "gemini-2.5-flash", Content: []AssistantContentBlock{
			ToolCall{ID: "t1", Name: "search", Arguments: JsonObject{"q": "X"}},
		}},
		ToolResultMessage{ToolCallID: "t1", ToolName: "search", Content: []ToolResultMessageContent{TextContent{Text: "found it"}}},
	}
	output := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	response := output[len(output)-1].Parts[0].FunctionResponse
	if response == nil || response.Name != "search" {
		t.Fatalf("function response = %#v", response)
	}
}
