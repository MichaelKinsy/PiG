package ai

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// strippedCatalogProviders are the built-in providers whose whole chat catalog is on one strippable API, with the error a
// request for such a provider reports once the API is stripped.
var strippedCatalogProviders = []struct {
	api      API
	provider string
	build    func() *ModelsProvider
	want     string
}{
	{APIBedrockConverseStream, "amazon-bedrock", AmazonBedrockProvider, "Provider amazon-bedrock: API bedrock-converse-stream is stripped from this Piglet (strip.apis: bedrock-converse-stream)"},
	{APIMistralConversations, "mistral", MistralProvider, "Provider mistral: API mistral-conversations is stripped from this Piglet (strip.apis: mistral-conversations)"},
	{APIGoogleVertex, "google-vertex", GoogleVertexProvider, "Provider google-vertex: API google-vertex is stripped from this Piglet (strip.apis: google-vertex)"},
}

// withoutPanic runs build and fails the test with the panic value, so a panic is one failed test and the rest of the package still runs.
func withoutPanic[T any](t *testing.T, what string, build func() T) T {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("%s panicked: %v", what, recovered)
		}
	}()
	return build()
}

// strippedCatalogModel is a chat model of provider's generated catalog, as a caller that kept one from before the strip holds it.
func strippedCatalogModel(t *testing.T, provider string) *Model {
	t.Helper()
	index := slices.IndexFunc(GeneratedModels, func(model GeneratedModel) bool { return model.Provider == provider })
	if index < 0 {
		t.Fatalf("the generated catalog has no %s model", provider)
	}
	generated := GeneratedModels[index]
	return &Model{ID: generated.ID, DisplayName: generated.DisplayName, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}, ProviderMeta: ProviderMetadata{ProviderID: provider, API: generated.API, BaseURL: "http://127.0.0.1:9"}}
}

// A Piglet's strip.apis, like a pig_strip_<api> Binary, takes every model from a provider whose whole catalog is on the stripped API.
// The built-in collection then holds no entry for that provider, and the provider's own constructor gives an entry without models
// whose streams report the strip. Neither panics for want of an API implementation.
func TestStrippedAPIBuiltinProviders(t *testing.T) {
	for _, tc := range strippedCatalogProviders {
		t.Run(string(tc.api), func(t *testing.T) {
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			providers := withoutPanic(t, "BuiltinProviders()", BuiltinProviders)
			ids := make([]string, len(providers))
			for i, provider := range providers {
				ids[i] = provider.ID
			}
			if want := slices.DeleteFunc(ListProviders(), ProviderStripped); !slices.Equal(ids, want) || slices.Contains(ids, tc.provider) {
				t.Fatalf("BuiltinProviders() = %v, want every provider but the stripped ones: %v", ids, want)
			}
			if entry := withoutPanic(t, "BuiltinModels()", func() *Models { return BuiltinModels() }).GetProvider(tc.provider); entry != nil {
				t.Fatalf("BuiltinModels() holds the stripped provider %s", tc.provider)
			}
			entry := withoutPanic(t, tc.provider+" constructor", tc.build)
			if entry == nil || entry.ID != tc.provider {
				t.Fatalf("%s constructor = %+v, want the provider's entry", tc.provider, entry)
			}
			if models, err := entry.GetAllModels(); err != nil || len(models) != 0 {
				t.Fatalf("%s offers %d models, %v; want none", tc.provider, len(models), err)
			}
			model := strippedCatalogModel(t, tc.provider)
			for name, stream := range map[string]func() (*AssistantMessageEventStream, error){
				"Stream": func() (*AssistantMessageEventStream, error) {
					return entry.Stream(t.Context(), model, NormalizeContext(Context{}), StreamOptions{})
				},
				"StreamSimple": func() (*AssistantMessageEventStream, error) {
					return entry.StreamSimple(t.Context(), model, NormalizeContext(Context{}), StreamOptions{})
				},
			} {
				if events, err := stream(); events != nil || err == nil || err.Error() != tc.want {
					t.Fatalf("%s.%s = %v, %v; want %q", tc.provider, name, events, err, tc.want)
				}
			}
		})
	}
}

// compat.ts stream looks the model's provider up in the built-in collection before every request. With an API stripped, a request
// to a provider that has nothing to do with it is still answered, and a request for a model of the stripped provider reports the strip.
func TestCompatStreamWithAStrippedAPI(t *testing.T) {
	for _, tc := range strippedCatalogProviders {
		t.Run(string(tc.api), func(t *testing.T) {
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			swap := func(models *Models) *Models {
				compatRegistry.mu.Lock()
				defer compatRegistry.mu.Unlock()
				previous := compatRegistry.compatMod
				compatRegistry.compatMod = models
				return previous
			}
			previous := swap(nil)
			t.Cleanup(func() { swap(previous) })
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(`data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}` + "\n\ndata: [DONE]\n\n"))
			}))
			t.Cleanup(server.Close)
			request := Context{Messages: []Message{UserMessage{Content: UserText("ping")}}}
			other := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}, ProviderMeta: ProviderMetadata{ProviderID: "probe-provider", API: APIOpenAICompletions, BaseURL: server.URL + "/v1"}}
			complete := func(model *Model) func() string {
				return func() string {
					message, err := Complete(t.Context(), model, request, StreamOptions{APIKey: "k"})
					if err != nil {
						return "error: " + err.Error()
					}
					return string(message.StopReason) + ": " + message.ErrorMessage
				}
			}
			if got := withoutPanic(t, "Complete on an openai-completions provider", complete(other)); got != "stop: " || requests.Load() != 1 {
				t.Fatalf("Complete on an unrelated provider = %q after %d requests, want the model's answer", got, requests.Load())
			}
			if got := withoutPanic(t, "Complete on a "+tc.provider+" model", complete(strippedCatalogModel(t, tc.provider))); got != "error: "+tc.want {
				t.Fatalf("Complete on a %s model = %q, want %q", tc.provider, got, "error: "+tc.want)
			}
		})
	}
}
