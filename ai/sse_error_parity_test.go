package ai

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestOpenAIStreamErrorMessageMatchesPinnedSDK(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		message string
		ok      bool
	}{
		{name: "message", raw: `{"message":"plain","type":"server_error"}`, message: "plain", ok: true},
		{name: "structured message", raw: `{"message":{"reason":"busy"}}`, message: `{"reason":"busy"}`, ok: true},
		{name: "object fallback", raw: `{"code":"busy"}`, message: `{"code":"busy"}`, ok: true},
		{name: "string fallback", raw: `"busy"`, message: `"busy"`, ok: true},
		{name: "null", raw: `null`},
		{name: "false", raw: `false`},
		{name: "zero", raw: `0`},
		{name: "empty string", raw: `""`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message, ok := openAIStreamErrorMessage([]byte(test.raw))
			if message != test.message || ok != test.ok {
				t.Fatalf("openAIStreamErrorMessage(%s) = %q, %t", test.raw, message, ok)
			}
		})
	}
}

func TestProvidersHandleInStreamErrorsLikePinnedSDKs(t *testing.T) {
	t.Run("openai-completions-message-only", func(t *testing.T) {
		result := runOpenAICompletionsSSE(t, "data: {\"error\":{\"message\":\"The server had an error while processing your request.\",\"type\":\"server_error\",\"param\":null,\"code\":null}}\n\ndata: [DONE]\n\n")
		if result.StopReason != StopReasonError || result.ErrorMessage != "The server had an error while processing your request." {
			t.Fatalf("result = %#v", result)
		}
		if IsRetryableAssistantError(*result) {
			t.Fatalf("SDK error.message should not become retryable through an added type: %q", result.ErrorMessage)
		}
	})

	t.Run("openai-completions-event-name-is-not-special", func(t *testing.T) {
		result := runOpenAICompletionsSSE(t, "event:error\ndata:{\"message\":\"ignored\"}\n\n"+
			"data:{\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\ndata:[DONE]\n\n")
		assertSSETextResult(t, result, "hi")
	})

	t.Run("openai-responses-message-only", func(t *testing.T) {
		result, _ := collectResponsesEvents(t, "data:{\"error\":{\"message\":\"Service overloaded\",\"code\":503}}\n\n")
		if result.StopReason != StopReasonError || result.ErrorMessage != "Service overloaded" {
			t.Fatalf("result = %#v", result)
		}
		if !IsRetryableAssistantError(*result) {
			t.Fatalf("error should remain retryable from its provider message: %#v", result)
		}
	})

	t.Run("codex-does-not-use-openai-sdk-error-object-rule", func(t *testing.T) {
		provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{APIKey: "test", Model: "model"}).(*openAIResponsesProvider)
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICodexResponses, "openai-codex", "model")
		provider.parseResponsesSSE(context.Background(), strings.NewReader("data:{\"error\":{\"message\":\"ignored\",\"code\":503}}\n\ndata:{\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"), builder, nil)
		if result := builder.stream.Result(); result.StopReason != StopReasonStop {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("google-sse-data-error-is-an-ordinary-response", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "model")
		provider := &googleProvider{}
		provider.parseGeminiSSE(context.Background(), strings.NewReader("event:error\ndata:{\"error\":{\"code\":503,\"message\":\"The model is overloaded\",\"status\":\"UNAVAILABLE\"}}\n\ndata:{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n"), builder)
		assertSSETextResult(t, builder.stream.Result(), "ok")
	})

	t.Run("google-raw-json-chunk-error", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIGoogleGenerativeAI, "google", "model")
		provider := &googleProvider{}
		provider.parseGeminiSSE(context.Background(), strings.NewReader(`{"error":{"code":503,"message":"The model is overloaded","status":"UNAVAILABLE"}}`), builder)
		result := builder.stream.Result()
		assertSSEErrorContains(t, result, `got status: UNAVAILABLE. {"error":{"code":503,"message":"The model is overloaded","status":"UNAVAILABLE"}}`)
		if !IsRetryableAssistantError(*result) {
			t.Fatalf("error should be retryable from status code: %#v", result)
		}
	})

	t.Run("mistral-requires-choices", func(t *testing.T) {
		builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
		provider := &mistralProvider{}
		provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader("data:{\"error\":{\"code\":503,\"message\":\"Service unavailable\"}}\n\ndata:{\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")), builder)
		assertSSEErrorContains(t, builder.stream.Result(), "Invalid Mistral streaming event")
	})
}
