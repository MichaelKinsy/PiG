package ai

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

func payloadHasZeroTemperature(payload any) bool {
	if input, ok := payload.(*bedrockruntime.ConverseStreamInput); ok {
		return input.InferenceConfig != nil && input.InferenceConfig.Temperature != nil && *input.InferenceConfig.Temperature == 0
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	var decoded any
	if json.Unmarshal(encoded, &decoded) != nil {
		return false
	}
	var visit func(any) bool
	visit = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if strings.EqualFold(key, "temperature") {
					number, ok := child.(float64)
					return ok && number == 0
				}
				if visit(child) {
					return true
				}
			}
		case []any:
			if slices.ContainsFunc(value, visit) {
				return true
			}
		}
		return false
	}
	return visit(decoded)
}

func TestProvidersSendExplicitZeroTemperature(t *testing.T) {
	providers := []struct {
		name     string
		provider Provider
		env      ProviderEnv
	}{
		{name: "anthropic", provider: NewAnthropicProvider(AnthropicConfig{APIKey: "test", Model: "claude-test", BaseURL: "https://example.test", ProviderID: "anthropic"})},
		{name: "amazon_bedrock", provider: NewBedrockProvider("anthropic.claude-test", "https://example.test"), env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}},
		{name: "openai_completions", provider: NewOpenAIProvider(OpenAIConfig{APIKey: "test", Model: "model", BaseURL: "https://example.test/v1", ProviderID: "openai"})},
		{name: "openai_responses", provider: NewOpenAIResponsesProvider(OpenAIResponsesConfig{APIKey: "test", Model: "model", BaseURL: "https://example.test/v1", ProviderID: "openai"})},
		{name: "google", provider: NewGoogleProvider(GoogleConfig{APIKey: "test", Model: "model", BaseURL: "https://example.test", ProviderID: "google"})},
		{name: "mistral", provider: NewMistralProvider(MistralConfig{APIKey: "test", Model: "model", BaseURL: "https://example.test", ProviderID: "mistral"})},
		{name: "pi_messages", provider: NewPiMessagesProvider(PiMessagesConfig{APIKey: "test", Model: "model", BaseURL: "https://example.test/v1", ProviderID: "radius"})},
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	captureError := errors.New("payload captured")
	for _, test := range providers {
		t.Run(test.name, func(t *testing.T) {
			capturedZero := false
			stream, err := test.provider.Stream(context.Background(), transcript, StreamOptions{
				Temperature:    0,
				TemperatureSet: true,
				Env:            test.env,
				OnPayload: func(payload any, _ *Model) (any, error) {
					capturedZero = payloadHasZeroTemperature(payload)
					return nil, captureError
				},
			})
			if err == nil && stream != nil {
				_ = stream.Result()
			} else if !errors.Is(err, captureError) {
				t.Fatalf("Stream error = %v, want payload capture", err)
			}
			if !capturedZero {
				t.Fatal("explicit temperature 0 was omitted from provider payload")
			}
		})
	}
}
