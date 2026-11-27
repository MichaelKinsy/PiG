package ai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// N5 & N6: ConvertResponsesMessages converts only messages (does not fail on invalid tool schemas),
// while PrepareResponsesPrompt also converts RequestTools and reports RequestEffortBaseline.
func TestPrepareResponsesPromptAndConvertResponsesMessagesSeparation(t *testing.T) {
	model := &Model{
		ID:    "gpt-5.4",
		Input: []string{"text"},
	}
	model.ProviderMeta.ProviderID = "openai-codex"
	model.ProviderMeta.API = APIOpenAICodexResponses
	model.ProviderMeta.Reasoning = true

	validTool := ToolSchema{
		Name:        "ping",
		Description: "ping tool",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
	}
	validCtx := NormalizeContext(Context{
		Messages: []Message{
			UserMessage{Content: UserText("hello"), Timestamp: 1},
		},
		Tools: []ToolSchema{validTool},
	})
	prepared, err := PrepareResponsesPrompt(model, validCtx, responsesToolCallProviders, ConvertResponsesMessagesOptions{
		ConfigurationUpdateEffort: "high",
		EffortBaseline:            "initial",
	})
	if err != nil {
		t.Fatalf("PrepareResponsesPrompt(valid): %v", err)
	}
	if len(prepared.Input) == 0 {
		t.Fatalf("expected non-empty Input")
	}
	if len(prepared.RequestTools) != 1 || prepared.RequestTools[0].Name != "ping" {
		t.Fatalf("RequestTools = %#v, want 1 tool named ping", prepared.RequestTools)
	}
	if prepared.RequestEffortBaseline != "high" {
		t.Fatalf("RequestEffortBaseline = %q, want %q", prepared.RequestEffortBaseline, "high")
	}

	// Tool with grammar constrained sampling fails ConvertResponsesTools when grammar tools are unsupported,
	// so PrepareResponsesPrompt rejects it, while ConvertResponsesMessages still succeeds (matching Pi convertResponsesMessages).
	badTool := ToolSchema{
		Name:                "broken",
		Description:         "broken tool",
		Parameters:          map[string]any{"type": "object"},
		ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: ConstrainedSamplingStrictRequire},
	}
	badCtx := NormalizeContext(Context{
		Messages: []Message{
			UserMessage{Content: UserText("hello"), Timestamp: 1},
		},
		Tools: []ToolSchema{badTool},
	})
	if _, err := PrepareResponsesPrompt(model, badCtx, responsesToolCallProviders, ConvertResponsesMessagesOptions{ToolOptions: ConvertResponsesToolsOptions{SupportsStrictMode: new(false)}}); err == nil {
		t.Fatalf("PrepareResponsesPrompt(bad tool) succeeded, want error")
	}
	input, err := ConvertResponsesMessages(model, badCtx, responsesToolCallProviders, ConvertResponsesMessagesOptions{ToolOptions: ConvertResponsesToolsOptions{SupportsStrictMode: new(false)}})
	if err != nil {
		t.Fatalf("ConvertResponsesMessages(bad tool) failed: %v", err)
	}
	if len(input) == 0 {
		t.Fatalf("ConvertResponsesMessages(bad tool) returned empty input")
	}
}

// N10: truthy non-string promptFeedback.blockReason is treated as a prompt block, matching Pi's truthiness check.
func TestGoogleStreamTruthyNonStringBlockReasonBlocksPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"promptFeedback":{"blockReason":42}}` + "\n\n"))
	}))
	defer server.Close()
	provider := NewGoogleProvider(GoogleConfig{APIKey: "test-key", Model: "test-model", BaseURL: server.URL})
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{
		Messages: []Message{UserMessage{Content: UserText("hello"), Timestamp: 1}},
	}), StreamOptions{})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	res := stream.Result()
	if res.StopReason != StopReasonError || !strings.Contains(res.ErrorMessage, "Google prompt blocked: 42") {
		t.Fatalf("result = stop=%q err=%q, want Google prompt blocked: 42", res.StopReason, res.ErrorMessage)
	}
}

// L7: OnResponse callback error on a retried attempt in OpenRouterImages is unwrapped to the bare message.
func TestOpenRouterImagesOnResponseCallbackErrorIsUnwrapped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()
	wantErr := "images callback exploded"
	model := ImageModel{
		ID:       "img-model",
		Provider: "openrouter",
		API:      "openrouter-images",
		BaseURL:  server.URL,
	}
	res := GenerateImagesOpenRouter(t.Context(), model, ImagesContext{Input: []ImagesInputContent{TextContent{Text: "a cat"}}}, ProviderImagesOptions{
		APIKey:     "k",
		MaxRetries: new(1),
		OnResponse: func(ProviderResponse, ImageModel) error {
			return errors.New(wantErr)
		},
	})
	if res.StopReason != ImagesStopReasonError || res.ErrorMessage != wantErr {
		t.Fatalf("res = stop=%q err=%q, want error / %q", res.StopReason, res.ErrorMessage, wantErr)
	}
}

// L14: classifierPost maps an OnResponse error to the timeout error when the attempt timer fired.
func TestClassifierPostOnResponseTimeoutReturnsTimeoutError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	timeoutMs := 15
	_, err := PostClassifierRequest(t.Context(), "test-classifier", server.URL, ClassifierModel{Provider: "test", ID: "m"}, map[string]any{"x": 1}, ClassifierOptions{
		APIKey:     "k",
		TimeoutMs:  &timeoutMs,
		MaxRetries: new(0),
		OnResponse: func(ProviderResponse, ClassifierModel) error {
			time.Sleep(40 * time.Millisecond)
			return errors.New("callback interrupted")
		},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout error", err)
	}
}
