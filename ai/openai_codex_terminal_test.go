package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICodexResponses_ResponseDoneNormalizesStatusAndEndTurn(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, fmt.Sprintf("data: %s\n\n", `{"type":"response.done","response":{"id":"resp_done","model":"gpt-5.2","status":"future_status","end_turn":true,"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`))
	}))
	defer server.Close()

	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{
		APIKey:     codexTestToken(t, "acct_terminal"),
		Model:      "gpt-5.2",
		ProviderID: "openai-codex",
		BaseURL:    server.URL,
	})
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{
		Messages: []Message{UserMessage{Content: UserText("hello")}},
	}), StreamOptions{Transport: TransportSSE})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop {
		t.Fatalf("stopReason = %q, want stop (error: %s)", result.StopReason, result.ErrorMessage)
	}
	if result.ResponseID != "resp_done" {
		t.Errorf("responseId = %q, want resp_done", result.ResponseID)
	}
	if result.RawStopReason != "" {
		t.Errorf("rawStopReason = %q, want empty normalized unknown status", result.RawStopReason)
	}
	if result.EndTurn == nil || !*result.EndTurn {
		t.Errorf("endTurn = %v, want true", result.EndTurn)
	}
}
