package coding

// pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// D36 wiring check, declaration -> carrier -> consumer through the production path: a provider registered with refreshModels is composed in the native
// collection (provider-composer.ts:613), and its Insecure opt-in must still reach the HTTP client that sends the request. A self-signed TLS endpoint is the
// observable: the request succeeds only once a registration has opted in, whichever registration form carried it; a later registration that omits the opt-in
// keeps it, as Pi keeps every field a re-registration leaves undefined (model-runtime.ts:921-933).
func TestRegisteredProviderInsecureReachesTheRequestWithRefreshModels(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"secured\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	refresh := func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) { return nil, nil }
	registrations := map[string]func(*AgentSessionServices, bool) error{
		"extension registerProvider with refreshModels": func(services *AgentSessionServices, insecure bool) error {
			return services.Registry().RegisterExtensionProvider("corp", extension.ProviderConfig{
				API: "openai-completions", BaseURL: server.URL, APIKey: "key", Insecure: insecure, RefreshModels: refresh,
				Models: []extension.ProviderModelConfig{{ID: "m", Name: "M", ContextWindow: 128, MaxTokens: 16}},
			})
		},
		"extension registerProvider": func(services *AgentSessionServices, insecure bool) error {
			return services.Registry().RegisterExtensionProvider("corp", extension.ProviderConfig{
				API: "openai-completions", BaseURL: server.URL, APIKey: "key", Insecure: insecure,
				Models: []extension.ProviderModelConfig{{ID: "m", Name: "M", ContextWindow: 128, MaxTokens: 16}},
			})
		},
		"native registerProvider": func(services *AgentSessionServices, insecure bool) error {
			model := nativeCompatModel("m", "corp", server.URL)
			return services.ModelRuntime().RegisterProvider("corp", ProviderConfigInput{
				API: ai.APIOpenAICompletions, BaseURL: server.URL, APIKey: "key", Insecure: insecure, Models: []ai.AnyModel{model},
				RefreshModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) { return nil, nil },
			})
		},
	}
	ask := func(services *AgentSessionServices) *ai.AssistantMessage {
		t.Helper()
		model := services.ModelRuntime().GetModel("corp", "m")
		if model == nil {
			t.Fatal("registered model missing")
		}
		return services.ModelRuntime().Complete(context.Background(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}, ai.StreamOptions{})
	}
	for name, register := range registrations {
		t.Run(name, func(t *testing.T) {
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			if err := register(services, false); err != nil {
				t.Fatal(err)
			}
			before := requests.Load()
			if result := ask(services); result.StopReason != ai.StopReasonError || requests.Load() != before {
				t.Fatalf("a registration without the opt-in reached the self-signed endpoint: stop=%q error=%q requests=%d", result.StopReason, result.ErrorMessage, requests.Load()-before)
			}
			// A re-registration's defined values replace the previous ones (Pi model-runtime.ts:921-933), so a later opt-in reaches the endpoint.
			if err := register(services, true); err != nil {
				t.Fatal(err)
			}
			before = requests.Load()
			if result := ask(services); result.StopReason != ai.StopReasonStop || requests.Load() != before+1 {
				t.Fatalf("a later registration that opts in did not reach the self-signed endpoint: stop=%q error=%q requests=%d", result.StopReason, result.ErrorMessage, requests.Load()-before)
			}
			// Its undefined values keep the previous ones, so a re-registration that omits the opt-in keeps it, as it keeps an omitted baseUrl.
			if err := register(services, false); err != nil {
				t.Fatal(err)
			}
			before = requests.Load()
			if result := ask(services); result.StopReason != ai.StopReasonStop || requests.Load() != before+1 {
				t.Fatalf("a re-registration that omits the opt-in dropped the earlier one: stop=%q error=%q requests=%d", result.StopReason, result.ErrorMessage, requests.Load()-before)
			}
		})
	}
}
