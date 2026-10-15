package ai

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// A per-model provider wrapped with its built-in stream wrapper must send every request through the provider's own HTTP client.
// Pi's cloudflareStreams (cloudflare-stream.ts:21-28) only swaps the model it dispatches; the API module keeps the fetch it was given. A
// request-scoped provider with a fresh client drops the configured transport and opens a new connection pool for each request. A provider
// without a registered wrapper keeps its endpoint as given, whatever the env holds.
func TestWrapBuiltinProviderSendsThroughTheProvidersClient(t *testing.T) {
	const template = "https://gateway.test/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}"
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		t.Run(string(api), func(t *testing.T) {
			var urls []string
			client := &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				urls = append(urls, request.URL.Scheme+"://"+request.URL.Host+request.URL.Path)
				reply := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				switch api {
				case APIOpenAIResponses:
					reply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
				case APIAnthropicMessages:
					reply = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
			})}
			var provider Provider
			suffix := "/chat/completions"
			switch api {
			case APIOpenAICompletions:
				p := NewOpenAIProvider(OpenAIConfig{BaseURL: template, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*openAIProvider)
				p.client = client
				provider = p
			case APIOpenAIResponses:
				p := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: template, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*openAIResponsesProvider)
				p.client = client
				provider = p
				suffix = "/responses"
			case APIAnthropicMessages:
				p := NewAnthropicProvider(AnthropicConfig{BaseURL: template, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*anthropicProvider)
				p.client = client
				provider = p
				suffix = "/v1/messages"
			}
			env := ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gate"}
			input := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
			for _, request := range []struct {
				providerID string
				env        ProviderEnv
			}{{"cloudflare-ai-gateway", env}, {"cloudflare-ai-gateway", nil}, {"custom", env}} {
				stream, err := WrapBuiltinProvider(request.providerID, provider).Stream(t.Context(), input, StreamOptions{Env: request.env})
				if err != nil {
					t.Fatal(err)
				}
				if result := stream.Result(); result == nil || result.StopReason != StopReasonStop {
					t.Fatalf("result=%#v", result)
				}
			}
			want := []string{"https://gateway.test/acct/gate" + suffix, template + suffix, template + suffix}
			if strings.Join(urls, "\n") != strings.Join(want, "\n") {
				t.Fatalf("requests through the provider's client = %q, want %q", urls, want)
			}
		})
	}
}

// The wrapper reads the env Pi's Models.applyAuth hands it: the auth resolution env under the request env (models.ts:868, then the
// provider's cloudflareStreams at cloudflare-stream.ts:23-26). A per-model provider holds the resolution env as its configured Env, so a
// request without an env still resolves the endpoint from it, and a request env wins per field.
func TestWrapBuiltinProviderResolvesTheEndpointFromTheConfiguredEnv(t *testing.T) {
	const template = "https://gateway.test/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}"
	configured := ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "stored-acct", "CLOUDFLARE_GATEWAY_ID": "stored-gate"}
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		t.Run(string(api), func(t *testing.T) {
			var urls []string
			client := &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				urls = append(urls, request.URL.Scheme+"://"+request.URL.Host+request.URL.Path)
				return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"fixture"}}`))}, nil
			})}
			var provider Provider
			suffix := "/chat/completions"
			switch api {
			case APIOpenAICompletions:
				p := NewOpenAIProvider(OpenAIConfig{BaseURL: template, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway", Env: configured}).(*openAIProvider)
				p.client = client
				provider = p
			case APIOpenAIResponses:
				p := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: template, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway", Env: configured}).(*openAIResponsesProvider)
				p.client = client
				provider = p
				suffix = "/responses"
			case APIAnthropicMessages:
				p := NewAnthropicProvider(AnthropicConfig{BaseURL: template, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway", Env: configured}).(*anthropicProvider)
				p.client = client
				provider = p
				suffix = "/v1/messages"
			}
			input := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}})
			for _, env := range []ProviderEnv{nil, {"CLOUDFLARE_GATEWAY_ID": "request-gate"}} {
				stream, err := WrapBuiltinProvider("cloudflare-ai-gateway", provider).Stream(t.Context(), input, StreamOptions{Env: env})
				if err != nil {
					t.Fatal(err)
				}
				stream.Result()
			}
			want := []string{"https://gateway.test/stored-acct/stored-gate" + suffix, "https://gateway.test/stored-acct/request-gate" + suffix}
			if strings.Join(urls, "\n") != strings.Join(want, "\n") {
				t.Fatalf("endpoints = %q, want %q", urls, want)
			}
		})
	}
}
