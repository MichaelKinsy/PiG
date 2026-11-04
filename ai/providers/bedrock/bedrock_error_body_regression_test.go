//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime"
)

func TestBedrockErrorBodyRegressionUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/provider-error-body-regression.test.ts:174
	t.Run("bedrock body-blind surfaces the gateway body instead of Unknown UnknownError", func(t *testing.T) {
		provider := NewBedrockProvider("us.anthropic.claude-opus-4-8", "")
		provider.converseStream = func(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
			return nil, ai.NewProviderError("UnknownError", new(403), `{"message":"blocked by gateway WAF"}`)
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

func providerErrorBodyResult(t *testing.T, provider ai.Provider) string {
	t.Helper()
	stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: "hi"}}}}}), ai.StreamOptions{Env: ai.ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}})
	// Go's direct provider setup failure is returned as an error; ModelRuntime turns it into the terminal ErrorEvent.
	if err != nil {
		return err.Error()
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonError {
		t.Fatalf("stopReason=%s", result.StopReason)
	}
	return result.ErrorMessage
}
