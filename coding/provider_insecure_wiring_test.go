package coding

// pi: packages/coding-agent/src/core/provider-composer.ts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// insecureState is what a registration says about insecure: nothing, false or true. The extension SDKs send it as the JSON member of the raw config, and
// the host decodes it as the three states, so the extension registrations below are built from JSON as the host receives them.
type insecureState string

const (
	insecureOmitted insecureState = ""
	insecureOff     insecureState = `,"insecure":false`
	insecureOn      insecureState = `,"insecure":true`
)

// D36 wiring check, declaration -> carrier -> consumer through the production path: a provider registered with refreshModels is composed in the native
// collection (provider-composer.ts:613), and its Insecure opt-in must still reach the HTTP client that sends the request. A self-signed TLS endpoint is the
// observable: the request succeeds only while the effective registration opts in, whichever registration form carried it. Pi's registerProvider merges a
// re-registration's defined values over the previous registration (model-runtime.ts:970-977): a later `insecure: true` turns the opt-in on, a later
// `insecure: false` turns it off again (the endpoint fails certificate verification), and an omitted one keeps whichever value was there.
func TestRegisteredProviderInsecureReachesTheRequestWithRefreshModels(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"secured\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	refresh := func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) { return nil, nil }
	extensionConfig := func(state insecureState, withRefresh bool) extension.ProviderConfig {
		var config extension.ProviderConfig
		raw := `{"api":"openai-completions","baseUrl":"` + server.URL + `","apiKey":"key","models":[{"id":"m","name":"M","contextWindow":128,"maxTokens":16}]` + string(state) + `}`
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
		if withRefresh {
			config.RefreshModels = refresh
		}
		return config
	}
	registrations := map[string]func(*AgentSessionServices, insecureState) error{
		"extension registerProvider with refreshModels": func(services *AgentSessionServices, state insecureState) error {
			return services.Registry().RegisterExtensionProvider("corp", extensionConfig(state, true))
		},
		"extension registerProvider": func(services *AgentSessionServices, state insecureState) error {
			return services.Registry().RegisterExtensionProvider("corp", extensionConfig(state, false))
		},
		"native registerProvider": func(services *AgentSessionServices, state insecureState) error {
			input := ProviderConfigInput{
				API: ai.APIOpenAICompletions, BaseURL: server.URL, APIKey: "key", Models: []ai.AnyModel{nativeCompatModel("m", "corp", server.URL)},
				RefreshModels: func(ai.RefreshModelsContext) ([]ai.AnyModel, error) { return nil, nil },
			}
			switch state {
			case insecureOn:
				input.SetInsecureSetting(new(true))
			case insecureOff:
				input.SetInsecureSetting(new(false))
			}
			return services.ModelRuntime().RegisterProvider("corp", input)
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
			reaches := func(step string, state insecureState, want bool) {
				t.Helper()
				if err := register(services, state); err != nil {
					t.Fatal(err)
				}
				before := requests.Load()
				result := ask(services)
				if want {
					if result.StopReason != ai.StopReasonStop || requests.Load() != before+1 {
						t.Fatalf("%s: the request did not reach the self-signed endpoint: stop=%q error=%q requests=%d", step, result.StopReason, result.ErrorMessage, requests.Load()-before)
					}
					return
				}
				if result.StopReason != ai.StopReasonError || requests.Load() != before {
					t.Fatalf("%s: the request reached the self-signed endpoint, whose certificate is not trusted: stop=%q error=%q requests=%d", step, result.StopReason, result.ErrorMessage, requests.Load()-before)
				}
			}
			reaches("a registration without the opt-in", insecureOmitted, false)
			// A re-registration's defined values replace the previous ones (Pi model-runtime.ts:970-977), so a later opt-in reaches the endpoint.
			reaches("a later registration that opts in", insecureOn, true)
			// Its undefined values keep the previous ones, so a re-registration that omits the opt-in keeps it, as it keeps an omitted baseUrl.
			reaches("a re-registration that omits the opt-in", insecureOmitted, true)
			// An explicit false is a defined value: certificates are verified again.
			reaches("a re-registration with insecure false", insecureOff, false)
			reaches("a re-registration that omits insecure after false", insecureOmitted, false)
			reaches("opting in again", insecureOn, true)
			reaches("insecure false once more", insecureOff, false)
		})
	}
}
