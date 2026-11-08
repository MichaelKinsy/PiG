package ai

import (
	"context"
	"sync"
	"testing"
)

// WrapModelAuth is the Go form of an application overriding `getAuth` on a Pi `Models` instance: the wrapper gets the
// resolution that was there and answers for chat models only.
func TestWrapModelAuthWrapsTheModelLevelResolution(t *testing.T) {
	models := CreateModels(CreateModelsOptions{Credentials: NewInMemoryCredentialStore()})
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("env-key")}}))
	model := modelsRuntimeModel("p1", "model-a")

	var overridesSeen AuthResolutionOverrides
	models.WrapModelAuth(func(previous ModelAuthFunc) ModelAuthFunc {
		return func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error) {
			overridesSeen = overrides
			result, err := previous(ctx, model, overrides)
			if err != nil || result == nil {
				return result, err
			}
			next := *result
			next.Auth.Headers = MergeProviderHeaders(result.Auth.Headers, ProviderHeaders{"X-Wrapped": new("w")})
			return &next, nil
		}
	})
	got, err := models.GetModelAuth(t.Context(), model, AuthResolutionOverrides{APIKey: new("explicit-key")})
	if err != nil || got == nil || got.Auth.APIKey != "explicit-key" || got.Auth.Headers["X-Wrapped"] == nil || *got.Auth.Headers["X-Wrapped"] != "w" {
		t.Fatalf("wrapped auth = %+v, %v", got, err)
	}
	if overridesSeen.APIKey == nil || *overridesSeen.APIKey != "explicit-key" {
		t.Fatalf("the wrapper saw overrides %+v", overridesSeen)
	}
	// The provider-level auth is not the model's: it is not wrapped.
	byID, err := models.GetAuth(t.Context(), "p1")
	if err != nil || byID == nil || byID.Auth.Headers["X-Wrapped"] != nil {
		t.Fatalf("provider auth = %+v, %v", byID, err)
	}
	// A second wrap wraps the first one.
	models.WrapModelAuth(func(previous ModelAuthFunc) ModelAuthFunc {
		return func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error) {
			result, err := previous(ctx, model, overrides)
			if err != nil || result == nil {
				return result, err
			}
			next := *result
			next.Auth.Headers = MergeProviderHeaders(result.Auth.Headers, ProviderHeaders{"X-Second": new("s")})
			return &next, nil
		}
	})
	got, err = models.GetModelAuth(t.Context(), model)
	if err != nil || got == nil || got.Auth.Headers["X-Wrapped"] == nil || got.Auth.Headers["X-Second"] == nil {
		t.Fatalf("twice wrapped auth = %+v, %v", got, err)
	}
}

// Wrapping while requests resolve auth is safe (run with -race).
func TestWrapModelAuthWhileRequestsResolve(t *testing.T) {
	models := CreateModels(CreateModelsOptions{Credentials: NewInMemoryCredentialStore()})
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "p1", auth: &ProviderAuth{APIKey: modelsRuntimeEnvKey("env-key")}}))
	model := modelsRuntimeModel("p1", "model-a")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 50 {
				if _, err := models.GetModelAuth(t.Context(), model); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	for range 20 {
		models.WrapModelAuth(func(previous ModelAuthFunc) ModelAuthFunc { return previous })
	}
	wg.Wait()
}
