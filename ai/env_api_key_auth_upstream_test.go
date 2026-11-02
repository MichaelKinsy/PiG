package ai

import (
	"context"
	"reflect"
	"testing"
)

// Ports packages/ai/test/providers.test.ts:455-485.
func TestEnvAPIKeyAuthUpstream(t *testing.T) {
	auth := EnvAPIKeyAuth("Test key", "FIRST_KEY", "SECOND_KEY")
	stored, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: providerTestAuthContext(map[string]string{"FIRST_KEY": "env"}), Credential: &Credential{Type: CredentialAPIKey, Key: "stored"}})
	if err != nil || stored == nil || stored.Auth.APIKey != "stored" || stored.Source != "stored credential" {
		t.Fatalf("stored=%+v %v", stored, err)
	}
	second, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: providerTestAuthContext(map[string]string{"SECOND_KEY": "second"})})
	if err != nil || second == nil || second.Auth.APIKey != "second" || second.Source != "SECOND_KEY" {
		t.Fatalf("second=%+v %v", second, err)
	}
	missing, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: providerTestAuthContext(nil)})
	if err != nil || missing != nil {
		t.Fatalf("missing=%+v %v", missing, err)
	}
	auth = EnvAPIKeyAuth("Test key", "TEST_KEY")
	credential, err := auth.Login(t.Context(), AuthInteraction{Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
		if _, ok := prompt.(AuthSecretPrompt); !ok {
			t.Fatalf("prompt=%T", prompt)
		}
		return "entered-key", nil
	}, Notify: func(AuthEvent) {}})
	if err != nil || !reflect.DeepEqual(credential, Credential{Type: CredentialAPIKey, Key: "entered-key"}) {
		t.Fatalf("credential=%+v %v", credential, err)
	}
}
