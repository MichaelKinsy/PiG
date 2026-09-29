package codingagent

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi model-runtime.ts:744-750 installs the object before starting availability refresh; a slower older refresh cannot replace a newer registration.
func TestNativeProviderRegistrationWinsBeforeAuthCompletes(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	makeProvider := func(model string) *extension.NativeProvider {
		return &extension.NativeProvider{ID: "replacement", Name: model, Models: []extension.ProviderModelConfig{{ID: model, API: ai.APIOpenAICompletions, BaseURL: "https://native.invalid"}},
			CheckAuth: func(context.Context, *ai.Credential) (*ai.AuthCheck, error) {
				return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
			},
			ResolveAuth: func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error) {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: "key"}}, nil, nil
			},
			ResolveRefreshCredential: func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error) { return nil, nil, nil },
			Stream: func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions, bool) (*ai.AssistantMessageEventStream, error) {
				return ai.NewAssistantMessageEventStream(), nil
			},
		}
	}
	first, second := makeProvider("old"), makeProvider("new")
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	first.CheckAuth = func(ctx context.Context, _ *ai.Credential) (*ai.AuthCheck, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return &ai.AuthCheck{Type: ai.CredentialAPIKey}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	// Registration returns without authenticating (model-runtime.ts:744-750); the queued refresh authenticates on a yield.
	if err := registry.RegisterNativeProvider(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
		t.Fatal("registration authenticated before a yield")
	default:
	}
	if registry.NativeProvider(first.ID) != first {
		t.Error("native object is not visible before authentication")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		registry.YieldToRegistrationRefresh(t.Context())
	}()
	<-entered
	if err := registry.RegisterNativeProvider(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	close(release)
	<-done
	if registry.NativeProvider(first.ID) != second || !registry.HasModelDefinition(first.ID, "new") || registry.HasModelDefinition(first.ID, "old") {
		t.Fatal("older authentication completion replaced the current Provider")
	}
}
