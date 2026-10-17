package coding

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestRefreshModelsRegistrationKeepsInsecure: a registration with refreshModels is composed in the native collection (provider-composer.ts:613), and its D36 TLS opt-in must still reach the request transport. The endpoint serves a self-signed certificate, so the request succeeds only when the opt-in survives.
func TestRefreshModelsRegistrationKeepsInsecure(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"tls\",\"choices\":[{\"delta\":{\"content\":\"over tls\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	// The default case's rejected handshake is expected; keep it out of the test log.
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name     string
		insecure bool
	}{{"opted-in", true}, {"default", false}} {
		t.Run(tc.name, func(t *testing.T) {
			services := newTestServices(t)
			config := extension.ProviderConfig{
				BaseURL:  server.URL + "/v1",
				APIKey:   "tls-key",
				API:      ai.APIOpenAICompletions,
				Insecure: tc.insecure,
				Models:   []extension.ProviderModelConfig{{ID: "tls-model", Name: "TLS Model"}},
				RefreshModels: func(ai.RefreshModelsContext) ([]extension.ProviderModelConfig, error) {
					return nil, nil
				},
			}
			if err := services.Registry().RegisterExtensionProvider("tls-refresh", config); err != nil {
				t.Fatal(err)
			}
			// A later partial re-registration keeps the earlier opt-in: Pi keeps a re-registration's undefined fields (model-runtime.ts:921-933).
			if err := services.Registry().RegisterExtensionProvider("tls-refresh", extension.ProviderConfig{Name: "TLS Refresh"}); err != nil {
				t.Fatal(err)
			}
			if input := services.Registry().GetRegisteredProviderConfig("tls-refresh"); input == nil || input.Insecure != tc.insecure {
				t.Fatalf("registered input = %+v, want Insecure %v", input, tc.insecure)
			}
			model := services.ModelRuntime().GetModel("tls-refresh", "tls-model")
			if model == nil {
				t.Fatal("the registered model is absent")
			}
			message := services.ModelRuntime().StreamSimple(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}, ai.StreamOptions{}).Result()
			if tc.insecure {
				if message.StopReason != ai.StopReasonStop || len(message.Content) != 1 || message.Content[0].(ai.TextContent).Text != "over tls" {
					t.Fatalf("insecure request = %+v", message)
				}
				return
			}
			// The OpenAI adapter reports a failed TLS handshake as the OpenAI SDK's "Connection error.".
			if message.StopReason != ai.StopReasonError || message.ErrorMessage != "Connection error." {
				t.Fatalf("secure request = %s %q, want the connection error of a rejected certificate", message.StopReason, message.ErrorMessage)
			}
		})
	}
}
