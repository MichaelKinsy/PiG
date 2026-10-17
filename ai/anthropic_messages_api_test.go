package ai

// Ports packages/ai/src/api/anthropic-messages.lazy.ts anthropicMessagesApi with the call of packages/ai/test/lazy-module-load.test.ts:76-93
// ("loads only the Anthropic SDK when streaming through the lazy API wrapper"): the API object's streamSimple, given an anthropic-messages model and a
// one-message context, runs the Anthropic Messages request and resolves its result. The dynamic-import wrapper itself is designed out (api_streams.go),
// so the test asserts what the wrapper delivers: the Anthropic request on the wire and the streamed answer.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mutation-checked: the mutant "AnthropicMessagesAPI builds the openai-completions API" fails it.
// mutation-checked: the mutant "AnthropicMessagesAPI.StreamSimple drops the context's messages" fails it.
func TestAnthropicMessagesAPIStreamsTheAnthropicRequestAndAnswer(t *testing.T) {
	type seen struct {
		path, key string
		body      map[string]any
	}
	requests := make(chan seen, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		requests <- seen{path: r.URL.Path, key: r.Header.Get("x-api-key"), body: body}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"test-model\",\"usage\":{\"input_tokens\":3}}}\n\n"+
			"event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"+
			"event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello from anthropic\"}}\n\n"+
			"event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n"+
			"event: message_delta\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\n"+
			"event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	t.Cleanup(server.Close)
	for form, stream := range map[string]APIStreamFunction{
		"stream":       AnthropicMessagesAPI().Stream,
		"streamSimple": AnthropicMessagesAPI().StreamSimple,
	} {
		model := compatTestModel("anthropic", APIAnthropicMessages)
		model.ProviderMeta.BaseURL = server.URL
		events, err := stream(t.Context(), model, NormalizeContext(compatHello), StreamOptions{APIKey: "key-1"})
		if err != nil || events == nil {
			t.Fatalf("%s = %v, %v", form, events, err)
		}
		result := events.Result()
		if result.StopReason != StopReasonStop || result.API != APIAnthropicMessages || len(result.Content) != 1 {
			t.Fatalf("%s result = %+v, want the finished anthropic-messages answer", form, result)
		}
		if text, ok := result.Content[0].(TextContent); !ok || text.Text != "hello from anthropic" {
			t.Fatalf("%s content = %#v", form, result.Content)
		}
		got := <-requests
		if !strings.HasSuffix(got.path, "/messages") || got.key != "key-1" {
			t.Fatalf("%s sent %q with x-api-key %q, want the Anthropic /messages request with the caller's key", form, got.path, got.key)
		}
		messages, _ := got.body["messages"].([]any)
		if len(messages) != 1 || !strings.Contains(string(mustJSON(t, messages[0])), "hi") {
			t.Fatalf("%s body messages = %v, want the context's one user message", form, got.body["messages"])
		}
	}
}
