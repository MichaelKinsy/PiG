package ai

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func dispatchTestModel(api API, id string) *Model {
	return &Model{ID: id, DisplayName: id, ProviderMeta: ProviderMetadata{API: api, ProviderID: "mixed", BaseURL: "https://example.test/v1"}, Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 10000, MaxOutputTokens: 1000}}
}
func configuredTestAuth() ProviderAuth {
	return ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:674
func TestProviderMissingAPIImplementationUpstream(t *testing.T) {
	provider := CreateProvider(CreateProviderOptions{ID: "mixed", Auth: configuredTestAuth(), Models: []*Model{dispatchTestModel("api-a", "model-a")}, API: ProviderAPIMap{"api-a": recordingProviderStreams("a", nil)}})
	stream, err := provider.StreamSimple(t.Context(), dispatchTestModel("api-ghost", "model-x"), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonError || !strings.Contains(result.ErrorMessage, "no API implementation") {
		t.Fatalf("result=%#v", result)
	}
}

func TestDefinedProviderCatalogSnapshotRoundTrip(t *testing.T) {
	model := dispatchTestModel("api-a", "model-a")
	model.ProviderMeta.Headers = map[string]string{"X-Model": "value"}
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingOff: nil, ThinkingHigh: new("verified")}
	model.InputLimits = &ModelInputLimits{MaxRequestBytes: 1024}
	raw, err := encodeModelsCatalog([]*Model{model})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeModelsCatalog(raw, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeModelsCatalog(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw, encoded) {
		t.Fatalf("snapshot differs: %s != %s", raw, encoded)
	}
	if len(decoded) != 1 || !reflect.DeepEqual(decoded[0].ThinkingLevelMap, model.ThinkingLevelMap) || !reflect.DeepEqual(decoded[0].ProviderMeta.Headers, model.ProviderMeta.Headers) {
		t.Fatal("metadata lost")
	}
}
