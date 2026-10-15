package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
)

func cloudflareGatewayModel() *Model {
	return &Model{ID: "model", DisplayName: "model", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, BaseURL: "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"}}
}

// capturingStreams records the base URL each entry point is dispatched with.
func capturingStreams(captured *[]string) *ProviderStreams {
	record := func(_ context.Context, model *Model, _ TranscriptContext, _ StreamOptions) (*AssistantMessageEventStream, error) {
		*captured = append(*captured, model.ProviderMeta.BaseURL)
		return nil, nil
	}
	return &ProviderStreams{Stream: record, StreamSimple: record}
}

// Ports packages/ai/test/cloudflare-stream.test.ts:23 "materializes the model endpoint before dispatch": both entry points of the wrapped API see the resolved endpoint.
func TestCloudflareStreamsMaterializeTheModelEndpointBeforeDispatch(t *testing.T) {
	var captured []string
	streams := CloudflareStreams(capturingStreams(&captured))
	env := ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "account", "CLOUDFLARE_GATEWAY_ID": "gateway"}
	model := cloudflareGatewayModel()
	if _, err := streams.Stream(t.Context(), model, TranscriptContext{}, StreamOptions{Env: env}); err != nil {
		t.Fatal(err)
	}
	if _, err := streams.StreamSimple(t.Context(), model, TranscriptContext{}, StreamOptions{Env: env}); err != nil {
		t.Fatal(err)
	}
	want := []string{"https://gateway.ai.cloudflare.com/v1/account/gateway/openai", "https://gateway.ai.cloudflare.com/v1/account/gateway/openai"}
	if !reflect.DeepEqual(captured, want) {
		t.Fatalf("dispatched endpoints = %v, want %v", captured, want)
	}
	if model.ProviderMeta.BaseURL != cloudflareGatewayModel().ProviderMeta.BaseURL {
		t.Fatal("the caller's model was mutated")
	}
}

// Ports packages/ai/test/cloudflare-stream.test.ts:49 "keeps placeholders when the provider env does not resolve them": no env, or an env without the values, dispatches the model unchanged.
func TestCloudflareStreamsKeepPlaceholdersWhenTheEnvDoesNotResolveThem(t *testing.T) {
	var captured []string
	streams := CloudflareStreams(capturingStreams(&captured))
	model := cloudflareGatewayModel()
	for _, env := range []ProviderEnv{nil, {}, {"UNRELATED": "x"}} {
		if _, err := streams.StreamSimple(t.Context(), model, TranscriptContext{}, StreamOptions{Env: env}); err != nil {
			t.Fatal(err)
		}
	}
	for _, got := range captured {
		if got != model.ProviderMeta.BaseURL {
			t.Fatalf("dispatched endpoint = %q, want the unresolved %q", got, model.ProviderMeta.BaseURL)
		}
	}
	if len(captured) != 3 {
		t.Fatalf("captured %d dispatches, want 3", len(captured))
	}
}

// cloudflare-stream.ts:6-17: a present-but-empty value replaces its placeholder with the empty string, one value may be missing while the other resolves, and a model
// without placeholders is returned as the same object.
func TestResolveCloudflareModelResolvesEachPlaceholderIndependently(t *testing.T) {
	model := cloudflareGatewayModel()
	partial := ResolveCloudflareModel(model, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct"})
	if got, want := partial.ProviderMeta.BaseURL, "https://gateway.ai.cloudflare.com/v1/acct/{CLOUDFLARE_GATEWAY_ID}/openai"; got != want {
		t.Fatalf("partial env: %q, want %q", got, want)
	}
	empty := ResolveCloudflareModel(model, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "", "CLOUDFLARE_GATEWAY_ID": "g"})
	if got, want := empty.ProviderMeta.BaseURL, "https://gateway.ai.cloudflare.com/v1//g/openai"; got != want {
		t.Fatalf("empty value: %q, want %q", got, want)
	}
	// String.prototype.replaceAll expands $& to the matched placeholder (cloudflare-stream.ts:12).
	dollar := ResolveCloudflareModel(model, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "a$&", "CLOUDFLARE_GATEWAY_ID": "$$"})
	if got, want := dollar.ProviderMeta.BaseURL, "https://gateway.ai.cloudflare.com/v1/a{CLOUDFLARE_ACCOUNT_ID}/$/openai"; got != want {
		t.Fatalf("replacement patterns: %q, want %q", got, want)
	}
	plain := &Model{ID: "m", ProviderMeta: ProviderMetadata{BaseURL: "https://example.test/v1"}}
	if ResolveCloudflareModel(plain, ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "a"}) != plain {
		t.Fatal("a model without placeholders must be returned as is")
	}
}

// providers/cloudflare-workers-ai.ts:20 and cloudflare-ai-gateway.ts:22-24 wrap their API streams with cloudflareStreams; other providers do not.
func TestOnlyTheCloudflareProvidersWrapTheirStreamsWithCloudflareStreams(t *testing.T) {
	for id, wrapped := range map[string]bool{"cloudflare-workers-ai": true, "cloudflare-ai-gateway": true, "groq": false, "opencode": false} {
		var captured []string
		streams := withBuiltinStreamWrapper(id, capturingStreams(&captured))
		if _, err := streams.StreamSimple(t.Context(), cloudflareGatewayModel(), TranscriptContext{}, StreamOptions{Env: ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "acct", "CLOUDFLARE_GATEWAY_ID": "gw"}}); err != nil {
			t.Fatal(err)
		}
		resolved := captured[0] == "https://gateway.ai.cloudflare.com/v1/acct/gw/openai"
		if resolved != wrapped {
			t.Errorf("%s: endpoint resolved before dispatch = %v, want %v (%q)", id, resolved, wrapped, captured[0])
		}
	}
}

// Pi's API modules (openAICompletionsApi, openAIResponsesApi, anthropicMessagesApi) send the model's base URL as given; only the provider's
// cloudflareStreams wrapper materializes the placeholders, once (cloudflare-stream.ts:21-29, cloudflare-ai-gateway.ts:22-24). Probed on Pi 1.1.0:
// with CLOUDFLARE_ACCOUNT_ID="a$&" the raw API requests /{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/... and the provider /a{CLOUDFLARE_ACCOUNT_ID}/g/...;
// with CLOUDFLARE_GATEWAY_ID="{CLOUDFLARE_ACCOUNT_ID}" the provider requests /a/{CLOUDFLARE_ACCOUNT_ID}/.... A second substitution would expand either again.
func TestCloudflarePlaceholdersMaterializeOnceOnlyThroughTheProviderWrapper(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.EscapedPath())
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture"}}`)
	}))
	defer server.Close()
	suffixes := map[API]string{APIOpenAICompletions: "/chat/completions", APIOpenAIResponses: "/responses", APIAnthropicMessages: "/v1/messages"}
	raw := "/%7BCLOUDFLARE_ACCOUNT_ID%7D/%7BCLOUDFLARE_GATEWAY_ID%7D"
	for _, tc := range []struct {
		env      ProviderEnv
		provider string
	}{
		{ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "a$&", "CLOUDFLARE_GATEWAY_ID": "g"}, "/a%7BCLOUDFLARE_ACCOUNT_ID%7D/g"},
		{ProviderEnv{"CLOUDFLARE_ACCOUNT_ID": "a", "CLOUDFLARE_GATEWAY_ID": "{CLOUDFLARE_ACCOUNT_ID}"}, "/a/%7BCLOUDFLARE_ACCOUNT_ID%7D"},
	} {
		for _, api := range []API{APIOpenAICompletions, APIOpenAIResponses, APIAnthropicMessages} {
			model := &Model{ID: "m", DisplayName: "m", ProviderMeta: ProviderMetadata{API: api, ProviderID: "cloudflare-ai-gateway", BaseURL: server.URL + "/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}"}}
			options := StreamOptions{APIKey: "k", Env: tc.env}
			for _, entry := range []struct {
				name string
				want string
				run  func() (*AssistantMessageEventStream, error)
			}{
				{"api module", raw, func() (*AssistantMessageEventStream, error) {
					return StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{}}), options)
				}},
				{"provider", tc.provider, func() (*AssistantMessageEventStream, error) {
					return CloudflareAIGatewayProvider().StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{}}), options)
				}},
				{"compat", tc.provider, func() (*AssistantMessageEventStream, error) {
					return StreamSimpleContext(t.Context(), model, Context{Messages: []Message{}}, options)
				}},
			} {
				mu.Lock()
				paths = nil
				mu.Unlock()
				stream, err := entry.run()
				if err != nil {
					t.Fatalf("%s %s: %v", api, entry.name, err)
				}
				stream.Result()
				mu.Lock()
				got := paths
				mu.Unlock()
				if want := []string{entry.want + suffixes[api]}; !reflect.DeepEqual(got, want) {
					t.Errorf("%s %s env=%v: requested %v, want %v", api, entry.name, tc.env, got, want)
				}
			}
		}
	}
}

// Pi's cloudflareStreams (cloudflare-stream.ts:19-29) returns an object with only stream and streamSimple, so the optional fetchDeferred and
// cancelDeferred of the wrapped ProviderStreams (types.ts:292-305) are dropped, and Models reports that the provider does not support deferred
// responses (models.ts:925-927). The wrapped value itself is left untouched.
// mutation-checked: copying the wrapped value (passing the deferred operations through), or replacing its Stream in place, fails it.
func TestCloudflareStreamsDropsDeferredOperationsAndLeavesTheWrappedValueUntouched(t *testing.T) {
	var captured []string
	inner := capturingStreams(&captured)
	inner.FetchDeferred = func(context.Context, *Model, DeferredHandle, DeferredFetchOptions) (*AssistantMessageEventStream, error) {
		return nil, nil
	}
	inner.CancelDeferred = func(context.Context, *Model, DeferredHandle, DeferredCancelOptions) error { return nil }
	stream := inner.Stream
	wrapped := CloudflareStreams(inner)
	if wrapped == inner {
		t.Fatal("the wrapper returned the wrapped value itself")
	}
	if reflect.ValueOf(inner.Stream).Pointer() != reflect.ValueOf(stream).Pointer() || inner.FetchDeferred == nil || inner.CancelDeferred == nil {
		t.Fatal("the wrapped value was changed")
	}
	if wrapped.Stream == nil || wrapped.StreamSimple == nil {
		t.Fatal("the wrapper lost stream or streamSimple")
	}
	if wrapped.FetchDeferred != nil || wrapped.CancelDeferred != nil {
		t.Fatal("the wrapper kept deferred operations that Pi's cloudflareStreams drops")
	}
	// Unwrapped streams stay nil rather than gaining a resolving wrapper.
	only := CloudflareStreams(&ProviderStreams{})
	if only.Stream != nil || only.StreamSimple != nil || only.FetchDeferred != nil || only.CancelDeferred != nil {
		t.Fatal("an empty implementation gained operations")
	}
}
