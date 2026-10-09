//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"io"
)

func TestProviderErrorBodyRegressionUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, provider, model, body string
		api                         API
		contains, absent            []string
		once                        string
	}{
		// .upstream/v0.87.1/packages/ai/test/provider-error-body-regression.test.ts:140
		{name: "openai-completions body-blind text surfaces status and body", provider: "openrouter", model: "test-model", api: APIOpenAICompletions, body: `{"error":"blocked by gateway WAF"}`, contains: []string{"403", "blocked by gateway WAF"}, absent: []string{"403 status code (no body)"}},
		// .upstream/v0.87.1/packages/ai/test/provider-error-body-regression.test.ts:149
		{name: "openai-completions does not double-print the OpenRouter metadata.raw extra", provider: "openrouter", model: "test-model", api: APIOpenAICompletions, body: `{"error":{"message":"Provider returned error","code":403,"metadata":{"raw":"upstream WAF blocked policy XYZ"}}}`, contains: []string{"upstream WAF blocked policy XYZ"}, once: "upstream WAF blocked policy XYZ"},
		// .upstream/v0.87.1/packages/ai/test/provider-error-body-regression.test.ts:166
		{name: "openai-responses status-only keeps the prefix and surfaces the body", provider: "openai", model: "gpt-test", api: APIOpenAIResponses, body: `{"error":"blocked by gateway WAF"}`, contains: []string{"OpenAI API error (403)", "blocked by gateway WAF"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			var provider Provider
			if tc.api == APIOpenAICompletions {
				provider = NewOpenAIProvider(OpenAIConfig{APIKey: "test", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			} else {
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test", ProviderID: tc.provider, Model: tc.model, BaseURL: server.URL})
			}
			message := providerErrorBodyResult(t, provider)
			for _, value := range tc.contains {
				if !strings.Contains(message, value) {
					t.Errorf("error %q missing %q", message, value)
				}
			}
			for _, value := range tc.absent {
				if strings.Contains(message, value) {
					t.Errorf("error %q contains %q", message, value)
				}
			}
			if tc.once != "" && strings.Count(message, tc.once) != 1 {
				t.Fatalf("duplicated reason: %q", message)
			}
		})
	}
	// .upstream/v0.87.1/packages/ai/test/provider-error-body-regression.test.ts:174
	t.Run("bedrock body-blind surfaces the gateway body instead of Unknown UnknownError", func(t *testing.T) {
		provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", "")
		provider.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
			return nil, &providerError{message: "UnknownError", status: new(403), body: `{"message":"blocked by gateway WAF"}`}
		}
		message := providerErrorBodyResult(t, provider)
		if !strings.Contains(message, "403") || !strings.Contains(message, "blocked by gateway WAF") || strings.Contains(message, "Unknown: UnknownError") {
			t.Fatal(message)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/provider-error-body-regression.test.ts:192
	t.Run("bedrock preserves the SDK validation message when the response body is a stream", func(t *testing.T) {
		provider := NewBedrockProvider("global.anthropic.claude-opus-5", "")
		provider.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
			return nil, &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"_readableState":{"buffer":[],"length":0}}`))}}, Err: &smithy.GenericAPIError{Code: "ValidationException", Message: "Invocation of model ID anthropic.claude-opus-5 with on-demand throughput isn't supported. Retry with an inference profile."}}
		}
		message := providerErrorBodyResult(t, provider)
		if !strings.Contains(message, "on-demand throughput isn't supported") || !strings.Contains(message, "inference profile") || strings.Contains(message, "_readableState") {
			t.Fatal(message)
		}
	})
}

func providerErrorBodyResult(t *testing.T, provider Provider) string {
	t.Helper()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserContentBlocks{TextContent{Text: "hi"}}}}}), StreamOptions{Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
	// Go's direct provider setup failure is returned as an error; ModelRuntime turns it into the terminal ErrorEvent.
	if err != nil {
		return err.Error()
	}
	result := stream.Result()
	if result.StopReason != StopReasonError {
		t.Fatalf("stopReason=%s", result.StopReason)
	}
	return result.ErrorMessage
}
