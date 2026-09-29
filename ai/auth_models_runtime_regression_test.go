package ai

import (
	"context"
	"errors"
	"testing"
)

func TestEnvAPIKeyAuthSkipsEmptyInjectedEnvironmentValues(t *testing.T) {
	// auth/helpers.ts:23-26 tests the resolved value, not merely presence in an injected environment.
	auth := EnvAPIKeyAuth("Key", "EMPTY", "FALLBACK")
	result, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: AuthContext{Env: func(name string) (string, bool) {
		if name == "EMPTY" {
			return "", true
		}
		return "fallback-key", true
	}}})
	if err != nil || result == nil || result.Auth.APIKey != "fallback-key" || result.Source != "FALLBACK" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	models := CreateModels(CreateModelsOptions{AuthContext: &AuthContext{Env: func(string) (string, bool) { return "", true }}})
	models.SetProvider(modelsRuntimeProvider(modelsRuntimeProviderInput{id: "empty", auth: &ProviderAuth{APIKey: EnvAPIKeyAuth("Key", "EMPTY")}}))
	available, err := models.GetAvailable(t.Context())
	if err != nil || len(available) != 0 {
		t.Fatalf("empty environment made provider available: %v, %v", available, err)
	}
}

func TestEnvAPIKeyAuthChecksAbortAfterEnvironmentCallback(t *testing.T) {
	// auth/helpers.ts:25 checks the signal after each environment lookup resolves.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	auth := EnvAPIKeyAuth("Key", "KEY")
	_, err := auth.Resolve(ctx, APIKeyAuthInput{Ctx: AuthContext{Env: func(string) (string, bool) { cancel(); return "must-not-use", true }}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}
