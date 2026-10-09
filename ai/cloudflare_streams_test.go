package ai

import (
	"context"
	"reflect"
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
