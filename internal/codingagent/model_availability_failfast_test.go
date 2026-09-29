package codingagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestRegistryAvailabilityBranchesRejectBeforeStalledAuth(t *testing.T) {
	for _, method := range []string{"auth checks", "available models"} {
		t.Run(method, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				registry := NewModelRegistry(t.TempDir())
				entered, release := make(chan struct{}), make(chan struct{})
				var startOnce, releaseOnce sync.Once
				releaseAll := func() { releaseOnce.Do(func() { close(release) }) }
				t.Cleanup(func() { releaseAll(); registry.CloseModelTasks() })
				failure := errors.New("auth failed before sibling")
				for _, id := range []string{"blocked", "failed"} {
					provider := &ai.ModelsProvider{ID: id, GetModels: func() ([]*ai.Model, error) {
						return []*ai.Model{{ID: "model", DisplayName: "Model", Input: []string{"text"}, ProviderMeta: ai.ProviderMetadata{ProviderID: id, API: ai.APIOpenAICompletions, BaseURL: "https://example.invalid"}, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 4096}}}, nil
					}, Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Check: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthCheck, error) {
						if id == "blocked" {
							startOnce.Do(func() { close(entered) })
							<-release
							return nil, nil
						}
						<-entered
						return nil, failure
					}}}}
					registry.NativeModels().SetProvider(provider)
				}
				done := make(chan error, 1)
				go func() {
					if method == "auth checks" {
						_, err := registry.GetProviderAuthChecks(t.Context())
						done <- err
					} else {
						_, err := registry.GetAvailableModelDataContext(t.Context(), "")
						done <- err
					}
				}()
				<-entered
				synctest.Wait()
				select {
				case err := <-done:
					if !errors.Is(err, failure) {
						t.Fatalf("result=%v, want %v", err, failure)
					}
				default:
					t.Error("nested availability Promise still waits for stalled auth after another provider failed")
				}
				releaseAll()
				synctest.Wait()
			})
		})
	}
}
