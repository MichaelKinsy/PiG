package ai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// Pi anthropic-messages.ts:580,596 passes the selected model data to both callbacks, not an ID/provider-only projection. This checks values, not pointer identity.
func TestAnthropicCallbacksReceiveSelectedModel(t *testing.T) {
	for _, shape := range []struct{ provider, key string }{
		{"anthropic", "test-key"},
		{"anthropic", "sk-ant-oat-test"},
		{"custom-proxy", "custom-key"},
	} {
		for _, reject := range []string{"payload", "response"} {
			t.Run(shape.provider+"/"+shape.key+"/"+reject, func(t *testing.T) {
				model := &Model{ID: "custom-model", DisplayName: "Custom Model", Input: []string{"text", "image"}, Capabilities: ModelCapabilities{ContextWindow: 123456, MaxOutputTokens: 8192, MaxThinking: ThinkingHigh}, ProviderMeta: ProviderMetadata{ProviderID: shape.provider, API: APIAnthropicMessages, BaseURL: "https://example.invalid", Reasoning: true, Headers: map[string]string{"X-Model": "selected"}, Compat: &ModelCompat{ForceAdaptiveThinking: new(true), SupportsMidConvoEffort: new(true)}}, ThinkingLevelMap: ThinkingLevelMap{ThinkingOff: nil, ThinkingLow: new("low")}}
				provider := NewAnthropicProvider(AnthropicConfig{Model: model.ID, ProviderID: shape.provider, APIKey: shape.key, BaseURL: model.ProviderMeta.BaseURL, ModelMetadata: model})
				defer func() { _ = provider.Close() }()
				var seen []string
				check := func(phase string, actual *Model) {
					t.Helper()
					seen = append(seen, phase)
					if !reflect.DeepEqual(actual, model) {
						t.Errorf("%s model=%#v, want complete selected model %#v", phase, actual, model)
					}
				}
				opts := StreamOptions{Fetch: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
				})}, OnPayload: func(_ any, actual *Model) (any, error) {
					check("payload", actual)
					if reject == "payload" {
						return nil, errors.New("rejected")
					}
					return nil, nil
				}, OnResponse: func(_ context.Context, _ ProviderResponse, actual *Model) error {
					check("response", actual)
					return errors.New("rejected")
				}}
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{}), opts)
				result := requireAnthropicSetupError(t, stream, err)
				wantSeen := []string{"payload"}
				if reject == "response" {
					wantSeen = append(wantSeen, "response")
				}
				if !reflect.DeepEqual(seen, wantSeen) || result.ErrorMessage != "rejected" {
					t.Fatalf("callbacks=%v result=%#v", seen, result)
				}
			})
		}
	}
}
