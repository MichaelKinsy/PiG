package ai

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// azure-openai-responses.ts createClient (:155-170) sends the model's headers and the caller's, and no session affinity headers: unlike
// openai-responses.ts buildHeaders (:277-286) it never sets session_id or x-client-request-id. The session id reaches only the
// prompt_cache_key (:202).
func TestAzureResponsesSendsNoSessionAffinityHeadersLikePi(t *testing.T) {
	headers := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(azureResponsesDoneSSE))
	}))
	defer server.Close()
	provider := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{APIKey: "k", Model: "gpt-4o-mini"})
	defer func() { _ = provider.Close() }()
	request := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}}})
	stream, err := provider.Stream(t.Context(), request, StreamOptions{AzureBaseURL: server.URL + "/openai/v1", SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonStop {
		t.Fatalf("result = %+v", result)
	}
	got := <-headers
	for _, name := range []string{"session_id", "x-client-request-id", "x-session-id", "x-session-affinity"} {
		if value := got.Get(name); value != "" {
			t.Errorf("%s = %q, want no header", name, value)
		}
	}
}
