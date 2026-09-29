package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
)

// Pi anthropic-messages.ts:201-203,1027,1199-1202 projects allowed fallback identities into the request and enables its beta unless configured headers replace the computed beta list. Provider/cost metadata is not request data.
func TestAnthropicAllowedFallbackRequestUpstream(t *testing.T) {
	first := AnthropicAllowedFallbackModel{Provider: "anthropic", Model: "fallback-a", Cost: ModelCost{Input: 3, Output: 5}}
	second := AnthropicAllowedFallbackModel{Provider: "another-provider", Model: "fallback-b", Cost: ModelCost{Input: 7, Output: 11}}
	for _, auth := range []struct{ name, provider, key, betaPrefix string }{
		{"api key", "anthropic", "test-key", ""},
		{"oauth", "anthropic", "sk-ant-oat-test", "claude-code-20250219,oauth-2025-04-20"},
		{"custom no default model", "custom-no-default", "test-key", ""},
	} {
		for _, tc := range []struct {
			name, want, beta string
			fallbacks        []AnthropicAllowedFallbackModel
			headers          ProviderHeaders
		}{
			{"omitted", "", "", nil, nil},
			{"empty", "", "", []AnthropicAllowedFallbackModel{}, nil},
			{"single", `[{"model":"fallback-a"}]`, "server-side-fallback-2026-07-01", []AnthropicAllowedFallbackModel{first}, nil},
			{"ordered", `[{"model":"fallback-b"},{"model":"fallback-a"}]`, "server-side-fallback-2026-07-01", []AnthropicAllowedFallbackModel{second, first}, nil},
			{"configured beta replaces computed", `[{"model":"fallback-a"}]`, "custom-beta", []AnthropicAllowedFallbackModel{first}, ProviderHeaders{"anthropic-beta": new("custom-beta, custom-beta")}},
			{"null beta suppresses computed", `[{"model":"fallback-a"}]`, "", []AnthropicAllowedFallbackModel{first}, ProviderHeaders{"anthropic-beta": nil}},
		} {
			t.Run(auth.name+"/"+tc.name, func(t *testing.T) {
				originalFallbacks := slices.Clone(tc.fallbacks)
				type request struct {
					body map[string]json.RawMessage
					beta string
				}
				captured := make(chan request, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var body map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					captured <- request{body, r.Header.Get("Anthropic-Beta")}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg\",\"model\":\"requested-model\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				}))
				defer server.Close()
				model := &Model{ID: "requested-model", Input: []string{"text"}, ProviderMeta: ProviderMetadata{ProviderID: auth.provider, API: APIAnthropicMessages, BaseURL: server.URL, Compat: &ModelCompat{AllowedFallbackModels: tc.fallbacks}}, Capabilities: ModelCapabilities{MaxOutputTokens: 1024, ContextWindow: 32768}}
				provider := NewAnthropicProvider(AnthropicConfig{ProviderID: auth.provider, Model: model.ID, BaseURL: server.URL, APIKey: auth.key, ModelMetadata: model})
				defer func() {
					if err := provider.Close(); err != nil {
						t.Error(err)
					}
				}()
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}), StreamOptions{Headers: tc.headers})
				if err != nil {
					t.Fatal(err)
				}
				if result := stream.Result(); result.StopReason != StopReasonStop {
					t.Fatalf("stream = %#v", result)
				}
				got := <-captured
				if !reflect.DeepEqual(tc.fallbacks, originalFallbacks) {
					t.Fatal("request projection mutated fallback identity/cost metadata")
				}
				if tc.want == "" {
					if _, exists := got.body["fallbacks"]; exists {
						t.Fatalf("empty fallback list must be omitted: %s", got.body["fallbacks"])
					}
				} else {
					if got.body["fallbacks"] == nil {
						t.Fatalf("missing fallbacks, want %s", tc.want)
					}
					assertShapeJSON(t, got.body["fallbacks"], tc.want)
				}
				wantBeta := tc.beta
				if auth.betaPrefix != "" && tc.headers == nil {
					wantBeta = auth.betaPrefix
					if tc.beta != "" {
						wantBeta += "," + tc.beta
					}
				}
				if got.beta != wantBeta {
					t.Fatalf("beta=%q, want %q", got.beta, wantBeta)
				}
			})
		}
	}
}
