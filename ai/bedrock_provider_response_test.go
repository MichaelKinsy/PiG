//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Ports packages/ai/test/bedrock-response-headers.test.ts:37.
func TestBedrockResponseHeadersUpstream(t *testing.T) {
	const modelID = "us.anthropic.claude-haiku-4-5-20251001-v1:0"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.Header().Set("x-bifrost-provider", "bedrock")
		w.Header().Set("x-bifrost-resolved-model", modelID)
		w.Header().Set("x-amzn-requestid", "req-123")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	model := mustGeneratedModel(t, "amazon-bedrock", modelID).ToModel()
	model.ProviderMeta.BaseURL = server.URL
	provider := NewBedrockProviderWithModel(*model)
	var responses []ProviderResponse
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{
		CacheRetention: CacheRetentionNone,
		Env:            ProviderEnv{"AWS_BEDROCK_FORCE_HTTP1": "1", "AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
		OnResponse: func(_ context.Context, response ProviderResponse, _ *Model) error {
			responses = append(responses, response)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result.StopReason != StopReasonError {
		t.Fatalf("empty event stream result = %#v", result)
	}
	if len(responses) != 1 || responses[0].Status != 200 {
		t.Fatalf("responses = %#v", responses)
	}
	for name, want := range map[string]string{"x-amzn-requestid": "req-123", "x-bifrost-provider": "bedrock", "x-bifrost-resolved-model": modelID} {
		if got := responses[0].Headers[name]; got != want {
			t.Errorf("header %s = %q, want %q", name, got, want)
		}
	}
}
