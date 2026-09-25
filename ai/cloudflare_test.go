package ai

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const cloudflareTestTemplate = "https://example.test/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/{CLOUDFLARE_ACCOUNT_ID}/{OTHER}/end"

type cloudflareEnvCase struct {
	name string
	env  ProviderEnv
	want string
}

func cloudflareEnvCases() []cloudflareEnvCase {
	return []cloudflareEnvCase{
		{"no env retains original", nil, cloudflareTestTemplate},
		{"empty env retains original", ProviderEnv{}, cloudflareTestTemplate},
		{"partial env retains unresolved", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "scoped"}, "https://example.test/scoped/{CLOUDFLARE_GATEWAY_ID}/scoped/{OTHER}/end"},
		{"empty value replaces placeholder", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "", "CLOUDFLARE_GATEWAY_ID": "gateway"}, "https://example.test//gateway//{OTHER}/end"},
		{"full env ignores unknown placeholder", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account", "CLOUDFLARE_GATEWAY_ID": "gateway", "OTHER": "ignored"}, "https://example.test/account/gateway/account/{OTHER}/end"},
		{"dollar escape", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "$$", "CLOUDFLARE_GATEWAY_ID": "gateway"}, "https://example.test/$/gateway/$/{OTHER}/end"},
		{"matched placeholder token", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "$&", "CLOUDFLARE_GATEWAY_ID": "gateway"}, "https://example.test/{CLOUDFLARE_ACCOUNT_ID}/gateway/{CLOUDFLARE_ACCOUNT_ID}/{OTHER}/end"},
		{"sequential replacement", ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "{CLOUDFLARE_GATEWAY_ID}", "CLOUDFLARE_GATEWAY_ID": "gateway"}, "https://example.test/gateway/gateway/gateway/{OTHER}/end"},
	}
}

// providers/cloudflare-stream.ts replaces only the two known placeholders, using
// supplied env without process fallback; missing and explicitly empty values differ.
func TestAIAuthCloudflareUnresolvedParity(t *testing.T) {
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "ambient-account")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "ambient-gateway")
	for _, tc := range cloudflareEnvCases() {
		t.Run(tc.name, func(t *testing.T) {
			before := maps.Clone(tc.env)
			got, err := ResolveCloudflareBaseURL("cloudflare-ai-gateway", cloudflareTestTemplate, tc.env)
			if err != nil || got != tc.want {
				t.Errorf("resolved = %q, %v; want %q, nil", got, err, tc.want)
			}
			if !reflect.DeepEqual(tc.env, before) {
				t.Fatal("mutated supplied env")
			}
		})
	}
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "")
	if got, err := ResolveCloudflareBaseURL("cloudflare-ai-gateway", cloudflareTestTemplate, ProviderEnv{}); err != nil || got != cloudflareTestTemplate {
		t.Errorf("missing values = %q, %v", got, err)
	}
	if got, err := ResolveCloudflareBaseURL("custom", cloudflareTestTemplate, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "ignored"}); err != nil || got != cloudflareTestTemplate {
		t.Errorf("non-Cloudflare = %q, %v", got, err)
	}
}

// String replaceAll uses JavaScript replacement tokens even though the search is not a regular expression.
func TestCloudflareReplacementStringTokens(t *testing.T) {
	const input = "L{CLOUDFLARE_ACCOUNT_ID}R{CLOUDFLARE_ACCOUNT_ID}T"
	for _, tc := range []struct{ value, want string }{
		{"$$", "L$R$T"},
		{"$&", input},
		{"$`", "LLRL{CLOUDFLARE_ACCOUNT_ID}RT"},
		{"$'", "LR{CLOUDFLARE_ACCOUNT_ID}TRTT"},
		{"$1", "L$1R$1T"},
		{"$<name>", "L$<name>R$<name>T"},
		{"$$&$", "L$&$R$&$T"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			got, err := ResolveCloudflareBaseURL("cloudflare-ai-gateway", input, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": tc.value})
			if err != nil || got != tc.want {
				t.Fatalf("replacement %q = %q, %v; want %q", tc.value, got, err, tc.want)
			}
		})
	}
}

// Cloudflare's API map wraps all three APIs. Inspect actual requests with a fake
// transport, not live Cloudflare, and reuse each provider to detect retained substitutions.
func TestCloudflareProductionRequestsUseExplicitEnv(t *testing.T) {
	t.Setenv("PI_CACHE_RETENTION", "none")
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "ambient-account")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "ambient-gateway")
	for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
		t.Run(string(api), func(t *testing.T) {
			var requestURL string
			var body map[string]any
			var headers http.Header
			client := &http.Client{Transport: openAITestRoundTripperFunc(func(request *http.Request) (*http.Response, error) {
				requestURL = request.URL.Scheme + "://" + request.URL.Host + request.URL.Path
				headers = request.Header.Clone()
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					return nil, err
				}
				if err := request.Body.Close(); err != nil {
					return nil, err
				}
				reply := "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
				if api == APIOpenAIResponses {
					reply = "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
				}
				if api == APIAnthropicMessages {
					reply = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"fixture\",\"usage\":{\"input_tokens\":1}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(reply))}, nil
			})}
			var provider Provider
			suffix := "/chat/completions"
			switch api {
			case APIOpenAICompletions:
				p := NewOpenAIProvider(OpenAIConfig{BaseURL: cloudflareTestTemplate, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*openAIProvider)
				p.client = client
				provider = p
			case APIOpenAIResponses:
				p := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: cloudflareTestTemplate, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*openAIResponsesProvider)
				p.client = client
				provider = p
				suffix = "/responses"
			case APIAnthropicMessages:
				p := NewAnthropicProvider(AnthropicConfig{BaseURL: cloudflareTestTemplate, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*anthropicProvider)
				p.client = client
				provider = p
				suffix = "/v1/messages"
			}
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			input := Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}
			for _, tc := range append(cloudflareEnvCases(), cloudflareEnvCases()[0]) {
				t.Run(tc.name, func(t *testing.T) {
					options := StreamOptions{Env: tc.env, MaxTokens: 123, Headers: ProviderHeaders{"X-Request": new("kept")}}
					before := maps.Clone(tc.env)
					requestURL = ""
					body = nil
					stream, err := provider.Stream(t.Context(), NormalizeContext(input), options)
					if err != nil {
						t.Fatal(err)
					}
					result := stream.Result()
					if result == nil || result.StopReason != StopReasonStop || result.Model != "test" || result.API != api || result.Provider != "cloudflare-ai-gateway" {
						t.Fatalf("result=%#v", result)
					}
					if requestURL != tc.want+suffix {
						t.Errorf("URL=%q, want %q", requestURL, tc.want+suffix)
					}
					if body["model"] != "test" || headers.Get("X-Request") != "kept" {
						t.Errorf("body=%v headers=%v", body, headers)
					}
					messagesKey, maxTokensKey := "messages", "max_tokens"
					var content any = "hello"
					if api == APIOpenAIResponses {
						messagesKey, maxTokensKey = "input", "max_output_tokens"
						content = []any{map[string]any{"type": "input_text", "text": "hello"}}
					}
					if !reflect.DeepEqual(body[messagesKey], []any{map[string]any{"role": "user", "content": content}}) || body[maxTokensKey] != float64(options.MaxTokens) {
						t.Errorf("context/options not forwarded: %v", body)
					}
					if !reflect.DeepEqual(tc.env, before) || !reflect.DeepEqual(options.Headers, ProviderHeaders{"X-Request": new("kept")}) || input.Messages[0].(UserMessage).Content != UserText("hello") {
						t.Fatal("mutated caller input")
					}
				})
			}
		})
	}
}
