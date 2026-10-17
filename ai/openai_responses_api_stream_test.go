package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// openai-responses.lazy.ts openAIResponsesApi().stream(model, context, options) is the openai-responses API: it POSTs the Responses request to
// <baseUrl>/responses with the bearer key (openai-responses.ts client.responses.create) and turns the SSE records into the assistant message.
// A stream of another API (for example azure-openai-responses, which needs an Azure endpoint and sends an api-key header) is not this API.
func TestOpenAIResponsesAPIStreamsThroughTheOpenAIResponsesEndpoint(t *testing.T) {
	const sse = "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\",\"status\":\"in_progress\",\"content\":[]}}\n\n" +
		"event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"output_index\":0,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\",\"annotations\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"hello\"}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"m1\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\",\"annotations\":[]}]}}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":7,\"output_tokens\":2,\"total_tokens\":9}}}\n\n"
	var mu sync.Mutex
	var paths []string
	var authorization []string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		authorization = append(authorization, r.Header.Get("Authorization"))
		_ = json.Unmarshal(data, &body)
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer server.Close()

	model := sharedModel("gpt-5", "openai", APIOpenAIResponses, true, []string{"text"}, nil)
	model.ProviderMeta.BaseURL = server.URL + "/v1"
	stream, err := OpenAIResponsesAPI().Stream(t.Context(), model, newTranscriptContext([]Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}), StreamOptions{APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	message := stream.Result()
	if message.StopReason != StopReasonStop || len(message.Content) != 1 {
		t.Fatalf("message = %+v", message)
	}
	if text, ok := message.Content[0].(TextContent); !ok || text.Text != "hello" {
		t.Fatalf("content = %#v, want the streamed text", message.Content)
	}
	if message.Usage.Input != 7 || message.Usage.Output != 2 {
		t.Fatalf("usage = %+v, want the response's input and output tokens", message.Usage)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/v1/responses" || authorization[0] != "Bearer sk-test" {
		t.Fatalf("request path %q authorization %q, want one POST to /v1/responses with the bearer key", paths, authorization)
	}
	if body["model"] != "gpt-5" || body["stream"] != true {
		t.Fatalf("request body = %v, want model gpt-5 and stream true", body)
	}
}
