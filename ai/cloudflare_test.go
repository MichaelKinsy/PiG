package ai

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

const cloudflareTestTemplate = "https://example.test/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/{CLOUDFLARE_ACCOUNT_ID}/{OTHER}/end"

// cloudflareResolve is resolveCloudflareModel (cloudflare-stream.ts:7-17) applied to a model with the template endpoint.
func cloudflareResolve(baseURL string, env ProviderEnv) string {
	model := &Model{ProviderMeta: ProviderMetadata{BaseURL: baseURL}}
	return ResolveCloudflareModel(model, env).ProviderMeta.BaseURL
}

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
			if got := cloudflareResolve(cloudflareTestTemplate, tc.env); got != tc.want {
				t.Errorf("resolved = %q; want %q", got, tc.want)
			}
			if !reflect.DeepEqual(tc.env, before) {
				t.Fatal("mutated supplied env")
			}
		})
	}
	t.Setenv("CLOUDFLARE_ACCOUNT_ID", "")
	t.Setenv("CLOUDFLARE_GATEWAY_ID", "")
	if got := cloudflareResolve(cloudflareTestTemplate, ProviderEnv{}); got != cloudflareTestTemplate {
		t.Errorf("missing values = %q", got)
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
			if got := cloudflareResolve(input, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": tc.value}); got != tc.want {
				t.Fatalf("replacement %q = %q; want %q", tc.value, got, tc.want)
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
			suffix := "/chat/completions"
			switch api {
			case APIOpenAIResponses:
				suffix = "/responses"
			case APIAnthropicMessages:
				suffix = "/v1/messages"
			}
			// The inner implementation reads only the model's endpoint, as an API module does: the wrapper has resolved it by then.
			inner := &ProviderStreams{Stream: func(ctx context.Context, model *Model, transcript TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
				var provider Provider
				switch api {
				case APIOpenAICompletions:
					p := NewOpenAIProvider(OpenAIConfig{BaseURL: model.ProviderMeta.BaseURL, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*openAIProvider)
					p.client = client
					provider = p
				case APIOpenAIResponses:
					p := NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: model.ProviderMeta.BaseURL, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*openAIResponsesProvider)
					p.client = client
					provider = p
				case APIAnthropicMessages:
					p := NewAnthropicProvider(AnthropicConfig{BaseURL: model.ProviderMeta.BaseURL, APIKey: "fixture", Model: "test", ProviderID: "cloudflare-ai-gateway"}).(*anthropicProvider)
					p.client = client
					provider = p
				}
				t.Cleanup(func() {
					if err := provider.Close(); err != nil {
						t.Error(err)
					}
				})
				return provider.Stream(ctx, transcript, options)
			}}
			streams := CloudflareStreams(inner)
			model := &Model{ID: "test", ProviderMeta: ProviderMetadata{ProviderID: "cloudflare-ai-gateway", API: api, BaseURL: cloudflareTestTemplate}}
			input := Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}
			for _, tc := range append(cloudflareEnvCases(), cloudflareEnvCases()[0]) {
				t.Run(tc.name, func(t *testing.T) {
					options := StreamOptions{Env: tc.env, MaxTokens: 123, Headers: ProviderHeaders{"X-Request": new("kept")}}
					before := maps.Clone(tc.env)
					requestURL = ""
					body = nil
					stream, err := streams.Stream(t.Context(), model, NormalizeContext(input), options)
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
					if api == APIAnthropicMessages {
						// anthropic-messages.ts:60-84: ambient env=none retains the short-cache breakpoint.
						content = []any{map[string]any{"type": "text", "text": "hello", "cache_control": map[string]any{"type": "ephemeral"}}}
					}
					if !reflect.DeepEqual(body[messagesKey], []any{map[string]any{"role": "user", "content": content}}) || body[maxTokensKey] != float64(options.MaxTokens) {
						t.Errorf("context/options not forwarded: %v", body)
					}
					if model.ProviderMeta.BaseURL != cloudflareTestTemplate {
						t.Fatalf("request mutated the model endpoint: %q", model.ProviderMeta.BaseURL)
					}
					if !reflect.DeepEqual(tc.env, before) || !reflect.DeepEqual(options.Headers, ProviderHeaders{"X-Request": new("kept")}) || input.Messages[0].(UserMessage).Content != UserText("hello") {
						t.Fatal("mutated caller input")
					}
				})
			}
		})
	}
}

// providers/cloudflare-workers-ai.ts:20 and cloudflare-ai-gateway.ts:22-24 wrap their APIs with cloudflareStreams; every other provider's API is
// dispatched with the endpoint it has. Registration decides, so no provider is recognized by its ID inside an API implementation.
func TestOnlyTheCloudflareProvidersWrapTheirStreams(t *testing.T) {
	inner := &ProviderStreams{Stream: StreamSimple, StreamSimple: StreamSimple}
	for _, id := range []string{"cloudflare-workers-ai", "cloudflare-ai-gateway"} {
		if got := withBuiltinStreamWrapper(id, inner); got == inner {
			t.Errorf("%s streams are not wrapped", id)
		}
	}
	for _, id := range []string{"openai", "anthropic", "openrouter", "cloudflare-other", "custom"} {
		if got := withBuiltinStreamWrapper(id, inner); got != inner {
			t.Errorf("%s streams are wrapped", id)
		}
	}
}

// cloudflare-stream.ts:19-31 cloudflareStreams resolves the model for both stream and streamSimple from options.env, passes the caller's
// model through when nothing changes, and never mutates it.
func TestCloudflareStreamsResolveTheModelForBothEntryPoints(t *testing.T) {
	var seen []*Model
	record := func(_ context.Context, model *Model, _ TranscriptContext, _ StreamOptions) (*AssistantMessageEventStream, error) {
		seen = append(seen, model)
		return nil, nil
	}
	wrapped := CloudflareStreams(&ProviderStreams{Stream: record, StreamSimple: record})
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{BaseURL: "https://gateway.test/{CLOUDFLARE_ACCOUNT_ID}/x"}}
	env := ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"}
	for name, call := range map[string]ModelsStreamFunction{"stream": wrapped.Stream, "streamSimple": wrapped.StreamSimple} {
		seen = nil
		if _, err := call(t.Context(), model, TranscriptContext{}, StreamOptions{Env: env}); err != nil {
			t.Fatal(err)
		}
		if len(seen) != 1 || seen[0] == model || seen[0].ProviderMeta.BaseURL != "https://gateway.test/acct/x" || seen[0].ID != "m" {
			t.Fatalf("%s dispatched %+v, want a copy of the model with the resolved endpoint", name, seen)
		}
		if model.ProviderMeta.BaseURL != "https://gateway.test/{CLOUDFLARE_ACCOUNT_ID}/x" {
			t.Fatalf("%s mutated the caller's model", name)
		}
		seen = nil
		if _, err := call(t.Context(), model, TranscriptContext{}, StreamOptions{}); err != nil {
			t.Fatal(err)
		}
		if len(seen) != 1 || seen[0] != model {
			t.Fatalf("%s without env dispatched %+v, want the caller's model", name, seen)
		}
	}
}
