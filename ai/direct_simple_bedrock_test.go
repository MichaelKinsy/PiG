package ai

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDirectSimpleBedrockCustomHeaders(t *testing.T) {
	// Copied from gate-close-03 TestBedrockUpstreamCustomHeaders/VC4's real caller guard; the middleware structure assertions remain in that lane.
	// packages/ai/test/bedrock-custom-headers.test.ts:185: streamSimple forwards headers without requiring an API key.
	for _, key := range []string{"AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_BEARER_TOKEN_BEDROCK", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		t.Setenv(key, "")
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "credentials"))
	server, requests := rejectingProviderServer(t)
	model := cloneGeneratedModel(t, "amazon-bedrock/us.anthropic.claude-opus-4-8").ToModel()
	model.ProviderMeta.BaseURL = server.URL
	options := StreamOptions{Headers: ProviderHeadersFromStrings(map[string]string{"x-custom": "v"}), Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}}
	stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), options)
	if stream != nil {
		if result := stream.Result(); result.StopReason != StopReasonError {
			t.Fatalf("expected mock send rejection, got %+v", result)
		}
	} else if err == nil {
		t.Fatal("expected mock send rejection")
	}
	select {
	case request := <-requests:
		if got := request.header.Get("x-custom"); got != "v" {
			t.Fatalf("wire x-custom = %q", got)
		}
		assertBedrockReservedHeadersOwnedBySDK(t, request, server.URL)
	default:
		t.Fatalf("request did not reach transport: %v", err)
	}
}

func TestDirectSimpleBedrockSelectedModel(t *testing.T) {
	// packages/ai/src/api/bedrock-converse-stream.ts:528-539: streamSimple passes the same model, options headers/env and optional generic apiKey to stream.
	for _, key := range []string{"", "generic-bearer-token"} {
		t.Run(key, func(t *testing.T) {
			model := &Model{ID: "custom-bedrock-model", DisplayName: "Custom Bedrock Model", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIBedrockConverseStream, ProviderID: "custom-provider", BaseURL: "https://example.invalid", Compat: &ModelCompat{SupportsStrictMode: new(true)}}, Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1234}}
			captured := errors.New("payload captured")
			called := false
			stream, err := StreamSimple(t.Context(), model, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hi")}}}), StreamOptions{APIKey: key, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1"}, OnPayload: func(_ any, got *Model) (any, error) {
				called = true
				if !reflect.DeepEqual(got, model) {
					t.Errorf("callback model = %+v, want %+v", got, model)
				}
				return nil, captured
			}})
			if !called || !errors.Is(err, captured) || stream != nil {
				t.Fatalf("called=%v stream=%v error=%v", called, stream != nil, err)
			}
		})
	}
}
