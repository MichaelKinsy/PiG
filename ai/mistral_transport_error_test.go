//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMistralTransportCancellationRemainsAborted(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		close(requestStarted)
		<-request.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	provider := NewMistralProvider(MistralConfig{BaseURL: server.URL, APIKey: "test-key", Model: "test-model"})
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
	stream, err := provider.Stream(ctx, transcript, StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	<-requestStarted
	cancel()

	resultReady := make(chan *AssistantMessage, 1)
	go func() { resultReady <- stream.Result() }()
	select {
	case result := <-resultReady:
		if result.StopReason != StopReasonAborted || IsRetryableAssistantError(*result) {
			t.Fatalf("result = reason %q error %q", result.StopReason, result.ErrorMessage)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Mistral stream did not observe cancellation")
	}
}
