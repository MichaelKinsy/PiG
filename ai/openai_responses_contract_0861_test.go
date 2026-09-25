package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestOpenAIResponsesRequestHeadersOverrideConfiguredHeaders(t *testing.T) {
	requestHeaders := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestHeaders <- request.Header.Clone()
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response-1\",\"model\":\"test\",\"status\":\"completed\"}}\n\n"))
	}))
	defer server.Close()

	provider := NewOpenAIResponsesProvider(OpenAIResponsesConfig{
		BaseURL:    server.URL,
		Model:      "test",
		ProviderID: "opencode",
		ExtraHeaders: map[string]string{
			"x-opencode-session": "configured-session",
			"X-Configured":       "configured-value",
		},
	})
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserText("hello")},
	}}), StreamOptions{Headers: ProviderHeadersFromStrings(map[string]string{
		"x-opencode-session": "request-session",
		"X-Request":          "request-value",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result == nil || result.StopReason != StopReasonStop {
		t.Fatalf("result = %#v", result)
	}

	headers := <-requestHeaders
	if got := headers.Get("x-opencode-session"); got != "request-session" {
		t.Fatalf("x-opencode-session = %q, want request-session", got)
	}
	if got := headers.Get("X-Configured"); got != "configured-value" {
		t.Fatalf("X-Configured = %q, want configured-value", got)
	}
	if got := headers.Get("X-Request"); got != "request-value" {
		t.Fatalf("X-Request = %q, want request-value", got)
	}
}

func runOpenAIResponsesEvents(t *testing.T, events string) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "test"}}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "test")
	go provider.parseResponsesSSE(context.Background(), strings.NewReader(events), builder, nil)
	result := builder.stream.Result()
	var got []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		got = append(got, event)
	}
	return result, got
}

func TestOpenAIResponsesFinalizesTextFromDoneItem(t *testing.T) {
	events := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"message-1","phase":"final_answer","content":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"message-1","phase":"final_answer","content":[{"type":"output_text","text":"final text"}]}}

data: {"type":"response.completed","response":{"id":"response-1","status":"completed"}}

`
	result, got := runOpenAIResponsesEvents(t, events)
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	text, ok := result.Content[0].(TextContent)
	if !ok || text.Text != "final text" || text.TextSignature != `{"v":1,"id":"message-1","phase":"final_answer"}` {
		t.Fatalf("text block = %#v", result.Content[0])
	}
	wantTypes := []AssistantEventType{EventStart, EventTextStart, EventTextEnd, EventDone}
	var types []AssistantEventType
	for _, event := range got {
		types = append(types, event.EventType())
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Fatalf("event types = %v, want %v", types, wantTypes)
	}
}

func TestOpenAIResponsesFinalizesReasoningFromDoneItem(t *testing.T) {
	events := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"reasoning-1","summary":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"reasoning-1","summary":[{"type":"summary_text","text":"reasoning summary"}],"encrypted_content":"opaque"}}

data: {"type":"response.completed","response":{"id":"response-1","status":"completed"}}

`
	result, _ := runOpenAIResponsesEvents(t, events)
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	thinking, ok := result.Content[0].(ThinkingContent)
	if !ok || thinking.Thinking != "reasoning summary" || !strings.Contains(thinking.ThinkingSignature, `"encrypted_content":"opaque"`) {
		t.Fatalf("thinking block = %#v", result.Content[0])
	}
}

func TestOpenAIResponsesBackfillsTerminalReasoningSignature(t *testing.T) {
	events := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"reasoning-1","summary":[]}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"reasoning-1","summary":[{"type":"summary_text","text":"summary"}]}}

data: {"type":"response.completed","response":{"id":"response-1","status":"completed","output":[{"type":"reasoning","id":"reasoning-1","summary":[{"type":"summary_text","text":"summary"}],"encrypted_content":"terminal-opaque"}]}}

`
	result, _ := runOpenAIResponsesEvents(t, events)
	thinking := result.Content[0].(ThinkingContent)
	if !strings.Contains(thinking.ThinkingSignature, `"encrypted_content":"terminal-opaque"`) {
		t.Fatalf("thinking signature = %q", thinking.ThinkingSignature)
	}
}

func TestOpenAIResponsesUsesDoneToolArgumentsNamespaceAndCompoundID(t *testing.T) {
	events := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":""}}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","namespace":"tools","arguments":"{\"path\":\"main.go\"}"}}

data: {"type":"response.completed","response":{"id":"response-1","status":"completed"}}

`
	result, _ := runOpenAIResponsesEvents(t, events)
	if len(result.Content) != 1 {
		t.Fatalf("content = %#v", result.Content)
	}
	tool, ok := result.Content[0].(ToolCall)
	if !ok || tool.ID != "call-1|item-1" || tool.Name != "read" || tool.Namespace != "tools" || !reflect.DeepEqual(tool.Arguments, JsonObject{"path": "main.go"}) {
		t.Fatalf("tool call = %#v", result.Content[0])
	}
}

func TestOpenAIResponsesCorrelatesInterleavedEventsByOutputIndex(t *testing.T) {
	events := `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":""}}

data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"message-1","content":[]}}

data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"message-1","content":[{"type":"output_text","text":"done"}]}}

data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"path\":\"main.go\"}"}

data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":"{\"path\":\"main.go\"}"}}

data: {"type":"response.completed","response":{"id":"response-1","status":"completed"}}

`
	result, _ := runOpenAIResponsesEvents(t, events)
	if len(result.Content) != 2 {
		t.Fatalf("content = %#v", result.Content)
	}
	tool, ok := result.Content[0].(ToolCall)
	if !ok || !reflect.DeepEqual(tool.Arguments, JsonObject{"path": "main.go"}) {
		t.Fatalf("tool call = %#v", result.Content[0])
	}
	text, ok := result.Content[1].(TextContent)
	if !ok || text.Text != "done" {
		t.Fatalf("text = %#v", result.Content[1])
	}
}

func TestOpenAIResponsesTerminalMetadataAndStopReasons(t *testing.T) {
	tests := []struct {
		name              string
		events            string
		wantReason        StopReason
		wantRaw           string
		wantError         string
		wantResponse      string
		wantResponseModel string
		wantTerminal      AssistantEventType
		wantUsage         bool
	}{
		{
			name: "completed",
			events: `data: {"type":"response.created","response":{"id":"response-1","model":"created-model","status":"in_progress"}}

data: {"type":"response.completed","response":{"id":"response-1","model":"completed-model","status":"completed"}}

`,
			wantReason: StopReasonStop, wantRaw: "completed", wantResponse: "response-1", wantResponseModel: "created-model", wantTerminal: EventDone,
		},
		{
			name: "max output incomplete",
			events: `data: {"type":"response.incomplete","response":{"id":"response-2","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}

`,
			wantReason: StopReasonLength, wantRaw: "incomplete.max_output_tokens", wantResponse: "response-2", wantTerminal: EventDone,
		},
		{
			name: "filtered incomplete",
			events: `data: {"type":"response.incomplete","response":{"id":"response-3","status":"incomplete","incomplete_details":{"reason":"content_filter"},"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}}

`,
			wantReason: StopReasonError, wantRaw: "incomplete.content_filter", wantError: "Response incomplete: content_filter", wantResponse: "response-3", wantTerminal: EventError, wantUsage: true,
		},
		{
			name: "failed",
			events: `data: {"type":"response.failed","response":{"id":"response-4","status":"failed","error":{"code":"server_error","message":"provider failed"},"usage":{"input_tokens":7,"output_tokens":2,"total_tokens":9}}}

`,
			wantReason: StopReasonError, wantRaw: "failed", wantError: "openai-responses: server_error: provider failed", wantResponse: "response-4", wantTerminal: EventError, wantUsage: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "test"}}
			builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "test")
			go provider.parseResponsesSSE(context.Background(), strings.NewReader(test.events), builder, nil)
			result := builder.stream.Result()
			if result.StopReason != test.wantReason || result.RawStopReason != test.wantRaw || result.ErrorMessage != test.wantError || result.ResponseID != test.wantResponse || result.ResponseModel != test.wantResponseModel {
				t.Fatalf("result = reason %q raw %q error %q response %q responseModel %q", result.StopReason, result.RawStopReason, result.ErrorMessage, result.ResponseID, result.ResponseModel)
			}
			if test.wantUsage && (result.Usage.Input != 7 || result.Usage.Output != 2 || result.Usage.TotalTokens != 9) {
				t.Fatalf("usage = %#v, want input 7 output 2 total 9", result.Usage)
			}
			var terminal AssistantEventType
			for event := range builder.stream.Events(context.Background()) {
				terminal = event.EventType()
			}
			if terminal != test.wantTerminal {
				t.Fatalf("terminal event = %q, want %q", terminal, test.wantTerminal)
			}
		})
	}
}

func TestOpenAIResponsesToolCallChangesCompletedStopToToolUse(t *testing.T) {
	events := `data: {"type":"response.output_item.added","item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":"{}"}}

data: {"type":"response.output_item.done","item":{"type":"function_call","id":"item-1","call_id":"call-1","name":"read","arguments":"{}"}}

data: {"type":"response.completed","response":{"id":"response-1","status":"completed"}}

`
	provider := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "test"}}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "test")
	go provider.parseResponsesSSE(context.Background(), strings.NewReader(events), builder, nil)
	result := builder.stream.Result()
	if result.StopReason != StopReasonToolUse {
		t.Fatalf("stop reason = %q, want %q", result.StopReason, StopReasonToolUse)
	}
}

func TestResponsesToolResultOutputJoinsTextWithNewlines(t *testing.T) {
	output := convertResponsesToolResultOutput([]ToolResultMessageContent{
		TextContent{Text: "one"}, TextContent{Text: "two"},
	}, false)
	var text string
	if err := json.Unmarshal(output, &text); err != nil {
		t.Fatal(err)
	}
	if text != "one\ntwo" {
		t.Fatalf("tool result output = %q, want newline-joined text", text)
	}
}

func TestResponsesToolResultOutputPreservesImageOnlyPlaceholder(t *testing.T) {
	output := convertResponsesToolResultOutput([]ToolResultMessageContent{
		ImageContent{MimeType: "image/png", Data: "AAAA"},
	}, false)
	var text string
	if err := json.Unmarshal(output, &text); err != nil {
		t.Fatal(err)
	}
	if text != "(see attached image)" {
		t.Fatalf("image-only output = %q", text)
	}
}
