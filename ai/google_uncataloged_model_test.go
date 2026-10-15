package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Gemini model outside the catalog still has the provider's API (every Pi Model has an api), so transformMessages' isSameModel
// (provider, api and id) sees the assistant message of the same provider and model as another API when the message names one and as the
// same model when it names none. Pi's provider-wire probe sends these two shapes to google.stream with a model it supplies.
func TestGoogleUncatalogedModelHasTheProvidersAPIForReplay(t *testing.T) {
	const model = "gemini-9-uncataloged"
	if _, ok := LookupModel("google/" + model); ok {
		t.Fatalf("%s is in the catalog", model)
	}
	body := func(messages []Message) string {
		var captured string
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			data, _ := io.ReadAll(request.Body)
			captured = string(data)
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: {\"candidates\":[{\"finishReason\":\"STOP\"}]}\n\n")
		}))
		defer server.Close()
		provider := NewGoogleProvider(GoogleConfig{APIKey: "k", Model: model, BaseURL: server.URL, ProviderID: "google"})
		stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: messages}), StreamOptions{MaxTokens: 64})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(context.Background()) {
		}
		return captured
	}

	t.Run("a message without an api is another model, so its tool-call ids are normalized", func(t *testing.T) {
		got := body(append([]Message{UserMessage{Content: UserText("hi")}}, googleToolCallMessagesFor(model)...))
		if !strings.Contains(got, `"id":"call_abc_item_def"`) || strings.Contains(got, "call_abc|item_def") {
			t.Fatalf("request body = %s, want the id normalized to call_abc_item_def", got)
		}
	})
	t.Run("a message with the provider's api is the same model, so its thought signature is replayed", func(t *testing.T) {
		got := body([]Message{UserMessage{Content: UserText("hi")}, AssistantMessage{Provider: "google", API: APIGoogleGenerativeAI, Model: model, Content: []AssistantContentBlock{
			ThinkingContent{Thinking: "reasoning", ThinkingSignature: "c2lnbmF0dXJl"}, TextContent{Text: "answer"},
		}}, UserMessage{Content: UserText("next")}})
		if !strings.Contains(got, `"thoughtSignature":"c2lnbmF0dXJl"`) {
			t.Fatalf("request body = %s, want the thought signature replayed", got)
		}
	})
}

func googleToolCallMessagesFor(model string) []Message {
	return []Message{
		AssistantMessage{Provider: "google", Model: model, Content: []AssistantContentBlock{ToolCall{ID: "call_abc|item_def", Name: "lookup", Arguments: JsonObject{"q": "x"}}}},
		ToolResultMessage{ToolCallID: "call_abc|item_def", ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "found"}}},
	}
}
