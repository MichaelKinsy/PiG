package coding

import (
	"os"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestSDKProviderAttributionUpstream(t *testing.T) {
	// D26 owns the product identity values; all routing and override assertions mirror Pi.
	router := map[string]string{"HTTP-Referer": "https://github.com/MichaelKinsy/PiG", "X-OpenRouter-Title": "PiG", "X-OpenRouter-Categories": "cli-agent"}
	nvidia := map[string]string{"X-BILLING-INVOKE-ORIGIN": "PiG"}
	for _, tc := range []struct {
		name, provider, url, modelID, sessionID string
		disabled                                bool
		providerHeaders, requestHeaders, want   map[string]string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:142
		{name: "adds default attribution headers for OpenRouter models", provider: "openrouter", url: "https://openrouter.ai/api/v1", want: router},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:150
		{name: "does not add attribution headers when telemetry is disabled", provider: "openrouter", url: "https://openrouter.ai/api/v1", disabled: true},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:160
		{name: "adds attribution headers for custom providers routed through OpenRouter", provider: "custom-openrouter", url: "https://openrouter.ai/api/v1", want: router},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:168
		{name: "preserves legacy OpenRouter base URL substring attribution matching", provider: "custom-openrouter", url: "not-a-url-openrouter.ai", want: router},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:176
		{name: "lets provider and request headers override the defaults", provider: "openrouter", url: "https://openrouter.ai/api/v1", providerHeaders: map[string]string{"HTTP-Referer": "https://provider.example", "X-OpenRouter-Categories": "provider-category"}, requestHeaders: map[string]string{"X-OpenRouter-Title": "request-title"}, want: map[string]string{"HTTP-Referer": "https://provider.example", "X-OpenRouter-Categories": "provider-category", "X-OpenRouter-Title": "request-title"}},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:192
		{name: "adds default attribution headers for direct NVIDIA NIM endpoints", provider: "custom-nim", url: "https://integrate.api.nvidia.com/v1", want: nvidia},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:198
		{name: "adds default attribution headers for the NVIDIA provider", provider: "nvidia", url: "https://example.test/v1", want: nvidia},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:204
		{name: "does not add NVIDIA NIM attribution headers when telemetry is disabled", provider: "nvidia", url: "https://integrate.api.nvidia.com/v1", disabled: true},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:212
		{name: "lets provider and request headers override NVIDIA NIM defaults", provider: "nvidia", url: "https://integrate.api.nvidia.com/v1", providerHeaders: map[string]string{"X-BILLING-INVOKE-ORIGIN": "Provider"}, requestHeaders: map[string]string{"X-BILLING-INVOKE-ORIGIN": "Request"}, want: map[string]string{"X-BILLING-INVOKE-ORIGIN": "Request"}},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:225
		{name: "does not add NVIDIA NIM attribution headers for NVIDIA models routed through OpenRouter", provider: "openrouter", url: "https://openrouter.ai/api/v1", modelID: "nvidia/nemotron-3-super-120b-a12b", want: router},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:234
		{name: "does not add NVIDIA NIM attribution headers for NVIDIA models routed through Vercel AI Gateway", provider: "vercel-ai-gateway", url: "https://ai-gateway.vercel.sh/v1", modelID: "nvidia/nemotron-3-super-120b-a12b"},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:242
		{name: "adds OpenCode session headers", provider: "opencode", url: "https://opencode.ai/zen/v1", sessionID: "opencode-session", want: map[string]string{"x-opencode-session": "opencode-session", "x-opencode-client": "pig"}},
		// .upstream/v0.87.1/packages/coding-agent/test/sdk-openrouter-attribution.test.ts:251
		{name: "lets configured OpenCode headers override the defaults", provider: "opencode", url: "https://opencode.ai/zen/v1", sessionID: "opencode-session", providerHeaders: map[string]string{"x-opencode-session": "configured-session", "x-opencode-client": "configured-client"}, want: map[string]string{"x-opencode-session": "configured-session", "x-opencode-client": "configured-client"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PI_TELEMETRY", "")
			if err := os.Unsetenv("PI_TELEMETRY"); err != nil {
				t.Fatal(err)
			}
			services := newTestServices(t)
			if err := services.SettingsManager().SetEnableInstallTelemetry(!tc.disabled); err != nil {
				t.Fatal(err)
			}
			modelID := tc.modelID
			if modelID == "" {
				modelID = tc.provider + "-test-model"
			}
			services.Registry().RegisterExtensionProvider(tc.provider, extension.ProviderConfig{API: ai.APIOpenAICompletions, APIKey: "test-api-key", BaseURL: tc.url, Headers: tc.providerHeaders, Models: []extension.ProviderModelConfig{{ID: modelID, Name: tc.provider + " Test Model", API: ai.APIOpenAICompletions, Input: []string{"text"}, ContextWindow: 128000, MaxTokens: 4096}}})
			model, err := BuildModel(tc.provider+"/"+modelID, services)
			if err != nil {
				t.Fatal(err)
			}
			// The upstream streamSimple stub captures after attribution without making HTTP requests.
			// Replace only the transport implementation, retaining BuildModel's actual attribution wrapper.
			wrapped, ok := model.Provider.(*providerAttributionProvider)
			if !ok {
				t.Fatalf("production model has no attribution wrapper: %T", model.Provider)
			}
			capture := &attributionCaptureProvider{}
			wrapped.Provider = capture
			session, err := NewSession(services, SessionOptions{Model: model, SessionID: tc.sessionID})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			stream, err := session.Model().Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{}), ai.StreamOptions{SessionID: session.ID(), Headers: ai.ProviderHeadersFromStrings(tc.requestHeaders)})
			if err != nil {
				t.Fatal(err)
			}
			if result := stream.Result(); result == nil || result.StopReason != ai.StopReasonStop {
				t.Fatalf("stream result=%+v", result)
			}
			if want := ai.ProviderHeadersFromStrings(tc.want); !reflect.DeepEqual(capture.options.Headers, want) {
				t.Fatalf("headers=%#v, want %#v", capture.options.Headers, want)
			}
		})
	}
}
