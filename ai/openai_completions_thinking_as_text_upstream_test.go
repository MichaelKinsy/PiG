package ai

// Ports packages/ai/test/openai-completions-thinking-as-text.test.ts (3 cases, Pi c23b9ec1). The Pi compat object sets
// requiresThinkingAsText and the defaults of every other flag, so the Go compat sets only requiresThinkingAsText.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func thinkingAsTextModel() *Model {
	model := completionsProofModel(true)
	model.ID = "repro-model"
	model.ProviderMeta.ProviderID = "repro-provider"
	return model
}

func thinkingAsTextContext(blocks ...AssistantContentBlock) Context {
	return Context{Messages: []Message{
		UserMessage{Content: UserText("hello"), Timestamp: 1},
		AssistantMessage{Content: blocks, API: APIOpenAICompletions, Provider: "repro-provider", Model: "repro-model", StopReason: StopReasonStop, Timestamp: 2},
		UserMessage{Content: UserText("continue"), Timestamp: 3},
	}}
}

func thinkingAsTextCompat() *OpenAICompat {
	yes := true
	return &OpenAICompat{RequiresThinkingAsText: &yes}
}

func TestOpenAICompletionsThinkingAsTextReplayUpstream(t *testing.T) {
	// openai-completions-thinking-as-text.test.ts:121
	t.Run("serializes same-model thinking-plus-text replay as assistant text parts", func(t *testing.T) {
		out, err := ConvertCompletionsMessages(thinkingAsTextModel(), NormalizeContext(thinkingAsTextContext(
			ThinkingContent{Thinking: "internal reasoning"},
			TextContent{Text: "visible answer"},
		)), thinkingAsTextCompat(), ConvertCompletionsMessagesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		assertCompletionsJSON(t, out[1], `{"role":"assistant","content":[{"type":"text","text":"internal reasoning"},{"type":"text","text":"visible answer"}]}`)
	})
	// openai-completions-thinking-as-text.test.ts:144
	t.Run("serializes same-model thinking-only replay as assistant text parts", func(t *testing.T) {
		out, err := ConvertCompletionsMessages(thinkingAsTextModel(), NormalizeContext(thinkingAsTextContext(
			ThinkingContent{Thinking: "internal reasoning"},
		)), thinkingAsTextCompat(), ConvertCompletionsMessagesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		assertCompletionsJSON(t, out[1], `{"role":"assistant","content":[{"type":"text","text":"internal reasoning"}]}`)
	})
	// openai-completions-thinking-as-text.test.ts:157
	t.Run("reaches the endpoint when replay contains both thinking and text", func(t *testing.T) {
		var bodies []map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			raw, _ := io.ReadAll(r.Body)
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("request body: %v", err)
			}
			bodies = append(bodies, body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			io.WriteString(w, `data: {"id":"chatcmpl-repro","object":"chat.completion.chunk","created":0,"model":"repro-model","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`+"\n\n")
			io.WriteString(w, `data: {"id":"chatcmpl-repro","object":"chat.completion.chunk","created":0,"model":"repro-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`+"\n\n")
			io.WriteString(w, "data: [DONE]\n\n")
		}))
		defer server.Close()
		p := NewOpenAIProvider(OpenAIConfig{Model: "repro-model", ProviderID: "repro-provider", BaseURL: server.URL, APIKey: "test-key", Compat: thinkingAsTextCompat()}).(*openAIProvider)
		stream, err := p.Stream(t.Context(), NormalizeContext(thinkingAsTextContext(
			ThinkingContent{Thinking: "internal reasoning"},
			TextContent{Text: "visible answer"},
		)), StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var last AssistantMessageEvent
		for event := range stream.Events(t.Context()) {
			last = event
		}
		if len(bodies) != 1 {
			t.Fatalf("request bodies = %d, want 1", len(bodies))
		}
		messages, _ := bodies[0]["messages"].([]any)
		want := map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "text", "text": "internal reasoning"},
			map[string]any{"type": "text", "text": "visible answer"},
		}}
		if len(messages) < 2 || !reflect.DeepEqual(messages[1], want) {
			t.Fatalf("messages[1] = %#v, want %#v", messages, want)
		}
		if _, ok := last.(DoneEvent); !ok {
			t.Fatalf("terminal event = %#v, want done", last)
		}
	})
}
