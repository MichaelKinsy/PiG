package ai

import (
	"context"
	"reflect"
	"testing"
)

func loginWithAnswers(t *testing.T, auth *APIKeyAuth, answers []string) (Credential, []AuthEvent) {
	t.Helper()
	if auth.Login == nil {
		t.Fatal("provider-owned login is missing")
	}
	events := []AuthEvent{}
	credential, err := auth.Login(t.Context(), AuthInteraction{Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
		if len(answers) == 0 {
			t.Fatalf("unexpected %s prompt", prompt.Type())
		}
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}, Notify: func(event AuthEvent) { events = append(events, event) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 0 {
		t.Fatal("not all prompts were awaited")
	}
	return credential, events
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:293
func TestProviderOwnedBedrockLoginUpstream(t *testing.T) {
	auth := bedrockAuth()
	bearer, _ := loginWithAnswers(t, auth, []string{"bearer-token", "bedrock-token"})
	if !reflect.DeepEqual(bearer, Credential{Type: CredentialAPIKey, Key: "bedrock-token"}) {
		t.Fatalf("bearer=%#v", bearer)
	}
	profile, events := loginWithAnswers(t, auth, []string{"aws-profile", "work"})
	if !reflect.DeepEqual(profile, Credential{Type: CredentialAPIKey, Env: map[string]string{"AWS_PROFILE": "work"}}) {
		t.Fatalf("profile=%#v", profile)
	}
	if len(events) != 1 {
		t.Fatalf("events=%v", events)
	}
	info, ok := events[0].(AuthInfoEvent)
	if !ok || len(info.Links) != 1 || info.Links[0].Label != "AWS credential provider chain" {
		t.Fatalf("event=%#v", events[0])
	}
	result, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: AuthContext{Env: func(string) (string, bool) { return "", false }}, Credential: &profile})
	if err != nil || result == nil || !reflect.DeepEqual(result.Auth, ModelAuth{}) || !reflect.DeepEqual(result.Env, profile.Env) {
		t.Fatalf("resolved=%#v error=%v", result, err)
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:387
func TestProviderOwnedVertexLoginUpstream(t *testing.T) {
	auth := vertexAuth()
	key, _ := loginWithAnswers(t, auth, []string{"api-key", "vertex-key"})
	if !reflect.DeepEqual(key, Credential{Type: CredentialAPIKey, Key: "vertex-key"}) {
		t.Fatalf("key=%#v", key)
	}
	adc, events := loginWithAnswers(t, auth, []string{"adc", "project-id", "us-central1"})
	want := map[string]string{"GOOGLE_CLOUD_PROJECT": "project-id", "GOOGLE_CLOUD_LOCATION": "us-central1"}
	if !reflect.DeepEqual(adc, Credential{Type: CredentialAPIKey, Env: want}) {
		t.Fatalf("adc=%#v", adc)
	}
	if len(events) != 1 {
		t.Fatalf("events=%v", events)
	}
	info, ok := events[0].(AuthInfoEvent)
	if !ok || len(info.Links) != 1 || info.Links[0].Label != "Application Default Credentials" {
		t.Fatalf("event=%#v", events[0])
	}
	result, err := auth.Resolve(t.Context(), APIKeyAuthInput{Ctx: AuthContext{Env: func(string) (string, bool) { return "", false }, FileExists: func(path string) bool { return path == "~/.config/gcloud/application_default_credentials.json" }}, Credential: &adc})
	if err != nil || result == nil || !reflect.DeepEqual(result.Auth, ModelAuth{}) || !reflect.DeepEqual(result.Env, want) {
		t.Fatalf("resolved=%#v error=%v", result, err)
	}
}

// .upstream/v0.87.1/packages/ai/test/providers.test.ts:474
func TestEnvAPIKeyAuthLoginUpstream(t *testing.T) {
	auth := EnvAPIKeyAuth("Test key", "TEST_KEY")
	if auth.Login == nil {
		t.Fatal("login is missing")
	}
	credential, err := auth.Login(t.Context(), AuthInteraction{Prompt: func(_ context.Context, prompt AuthPrompt) (string, error) {
		if prompt.Type() != "secret" {
			t.Fatal("prompt is not secret")
		}
		return "entered-key", nil
	}, Notify: func(AuthEvent) {}})
	if err != nil || !reflect.DeepEqual(credential, Credential{Type: CredentialAPIKey, Key: "entered-key"}) {
		t.Fatalf("credential=%#v error=%v", credential, err)
	}
}
