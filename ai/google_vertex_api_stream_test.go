//go:build !pig_strip_google_vertex

package ai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// google-vertex.lazy.ts googleVertexApi().stream(model, context, options) is the google-vertex API: in express mode (a real API key) it POSTs
// <baseUrl>/v1/publishers/google/models/<model>:streamGenerateContent?alt=sse with the x-goog-api-key header (google-vertex.ts, the
// google-vertex-api-key-resolution.test.ts cases) and turns the SSE records into the assistant message. google-generative-ai is a different API.
func TestGoogleVertexAPIStreamsThroughTheVertexExpressEndpoint(t *testing.T) {
	const sse = "data: {\"responseId\":\"vertex-r1\",\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hello\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":6,\"candidatesTokenCount\":3,\"totalTokenCount\":9}}\n\n"
	var mu sync.Mutex
	var urls, keys, bearer []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		urls = append(urls, r.URL.RequestURI())
		keys = append(keys, r.Header.Get("x-goog-api-key"))
		bearer = append(bearer, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, sse)
	}))
	defer server.Close()

	model := sharedModel("gemini-3-flash-preview", "google-vertex", APIGoogleVertex, false, []string{"text"}, nil)
	model.ProviderMeta.BaseURL = server.URL
	stream, err := GoogleVertexAPI().Stream(t.Context(), model, newTranscriptContext([]Message{UserMessage{Content: UserText("hi"), Timestamp: 1}}), StreamOptions{APIKey: "AIzaSyExampleRealisticLookingApiKey123456"})
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
	if message.Usage.Input != 6 || message.Usage.Output != 3 {
		t.Fatalf("usage = %+v, want the response's prompt and candidate tokens", message.Usage)
	}
	mu.Lock()
	defer mu.Unlock()
	const want = "/v1/publishers/google/models/gemini-3-flash-preview:streamGenerateContent?alt=sse"
	if len(urls) != 1 || urls[0] != want || keys[0] != "AIzaSyExampleRealisticLookingApiKey123456" || bearer[0] != "" {
		t.Fatalf("requests %q key %q authorization %q, want one POST to %s with the express key and no bearer token", urls, keys, bearer, want)
	}
}
