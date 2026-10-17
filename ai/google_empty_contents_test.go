package ai

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Pi's Google clients run the request through @google/genai, whose tContents throws "contents are required" for an undefined, null or empty `contents` before anything is sent (a conversation of system messages only has none: the leading prompt goes to systemInstruction). PiG fails the same way instead of sending a request the API rejects with HTTP 400.
func TestGoogleEmptyContentsFailsBeforeSending(t *testing.T) {
	model, ok := LookupModelExact("google/gemini-2.5-flash")
	if !ok {
		t.Fatal("model is not in the catalog")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	provider := newMatrixProvider(t, model, server.URL, false)
	t.Cleanup(func() { _ = provider.Close() })
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{SystemMessage{Content: SystemText("only a system prompt")}}}), StreamOptions{Transport: TransportSSE})
	var message *AssistantMessage
	if err == nil && stream != nil {
		message = stream.Result()
	}
	if requests.Load() != 0 {
		t.Fatalf("%d request(s) sent, want none: the SDK throws before sending", requests.Load())
	}
	switch {
	case err != nil:
		if err.Error() != "contents are required" {
			t.Fatalf("error = %q, want %q", err, "contents are required")
		}
	case message == nil || message.StopReason != StopReasonError || message.ErrorMessage != "contents are required":
		t.Fatalf("result = %#v, want an error turn \"contents are required\"", message)
	}
}

// The SDK validates the parameters onPayload returned (google-generative-ai.ts:96-100: generateContentStream(params) after the hook), so the check reads the payload as sent, not the converted history.
func TestGoogleEmptyContentsChecksThePayloadOnPayloadReturned(t *testing.T) {
	model, ok := LookupModelExact("google/gemini-2.5-flash")
	if !ok {
		t.Fatal("model is not in the catalog")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	provider := newMatrixProvider(t, model, server.URL, false)
	t.Cleanup(func() { _ = provider.Close() })
	withContents := func(contents any) func(any, *Model) (any, error) {
		return func(value any, _ *Model) (any, error) {
			payload := value.(map[string]any)
			payload["contents"] = contents
			return payload, nil
		}
	}
	systemOnly := NormalizeContext(Context{Messages: []Message{SystemMessage{Content: SystemText("only a system prompt")}}})
	added := []map[string]any{{"role": "user", "parts": []map[string]any{{"text": "added by the hook"}}}}
	if _, err := provider.Stream(t.Context(), systemOnly, StreamOptions{Transport: TransportSSE, OnPayload: withContents(added)}); err != nil && err.Error() == "contents are required" {
		t.Fatalf("contents added by onPayload were rejected: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("%d request(s) sent, want 1 for contents added by onPayload", requests.Load())
	}
	prompt := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	_, err := provider.Stream(t.Context(), prompt, StreamOptions{Transport: TransportSSE, OnPayload: withContents([]any{})})
	if err == nil || err.Error() != "contents are required" || requests.Load() != 1 {
		t.Fatalf("contents emptied by onPayload: err=%v, %d request(s) sent; want \"contents are required\" and no new request", err, requests.Load())
	}
}
