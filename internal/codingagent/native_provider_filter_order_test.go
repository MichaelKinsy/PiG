package codingagent

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/ai/src/models.ts:731-734 (getAllAvailable): filterAllModels receives `provider.getAllModels?.() ?? provider.getModels()`, the
// provider's own model objects, so an extension that reads their members sees the order it wrote them in.
func TestNativeProviderFilterAllModelsSeesTheProvidersOwnModels(t *testing.T) {
	const record = `{"id":"probe-model","name":"Probe Model","provider":"probe-native","api":"openai-completions","baseUrl":"https://probe.invalid","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}`
	model, known, err := ai.DecodeStoredModel([]byte(record))
	if err != nil || !known {
		t.Fatalf("DecodeStoredModel: known %v, err %v", known, err)
	}
	var mu sync.Mutex
	var seen []string
	provider := &extension.NativeProvider{
		ID: "probe-native", Name: "Probe Native", BaseURL: "https://probe.invalid",
		CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) { return &ai.AuthCheck{}, nil },
		Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return nil, nil
		},
		ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
			return nil, nil, nil
		},
		ResolveRefreshCredential: func(_ context.Context, current *ai.Credential) (*ai.Credential, *ai.Credential, error) {
			return current, nil, nil
		},
		GetAllModels: func(context.Context) ([]ai.AnyModel, error) { return []ai.AnyModel{model}, nil },
		FilterAllModels: func(_ context.Context, models []ai.AnyModel, _ *ai.Credential) ([]ai.AnyModel, error) {
			encoded, err := ai.EncodeModelsCatalog(models)
			if err != nil {
				return nil, err
			}
			mu.Lock()
			defer mu.Unlock()
			for _, raw := range encoded {
				seen = append(seen, string(raw))
			}
			return models, nil
		},
	}
	registry := NewModelRegistry(t.TempDir())
	registry.SetCredentialStore(ai.NewInMemoryAuthStorage(map[string]ai.Credential{}))
	ctx := context.Background()
	if err := registry.RegisterNativeProvider(ctx, provider); err != nil {
		t.Fatal(err)
	}
	registry.YieldToRegistrationRefresh(ctx)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("filterAllModels never ran")
	}
	for _, got := range seen {
		if got != record {
			t.Fatalf("filterAllModels saw %s, want the provider's model %s", got, record)
		}
	}
}
