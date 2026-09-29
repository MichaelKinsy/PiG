package ai

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestProviderLoginCancellationAndPromptFailure(t *testing.T) {
	for _, auth := range []*APIKeyAuth{EnvAPIKeyAuth("Test key", "TEST_KEY"), bedrockAuth(), vertexAuth()} {
		t.Run(auth.Name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			called := false
			interaction := AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { called = true; return "ignored", nil }, Notify: func(AuthEvent) { called = true }}
			if _, err := auth.Login(ctx, interaction); !errors.Is(err, context.Canceled) || called {
				t.Fatalf("pre-cancel: error=%v called=%v", err, called)
			}
			ctx, cancel = context.WithCancel(t.Context())
			interaction.Prompt = func(context.Context, AuthPrompt) (string, error) { cancel(); return "ignored", nil }
			if _, err := auth.Login(ctx, interaction); !errors.Is(err, context.Canceled) || called {
				t.Fatalf("post-prompt cancel: error=%v notify=%v", err, called)
			}
			sentinel := errors.New("prompt rejected")
			interaction.Prompt = func(context.Context, AuthPrompt) (string, error) { return "", sentinel }
			if _, err := auth.Login(t.Context(), interaction); !errors.Is(err, sentinel) {
				t.Fatalf("prompt rejection=%v", err)
			}
		})
	}
}

func TestAuthInteractionDiscriminatedJSON(t *testing.T) {
	cases := []struct {
		value any
		kind  string
	}{
		{AuthTextPrompt{Message: "text"}, "text"}, {AuthSecretPrompt{Message: "secret"}, "secret"}, {AuthManualCodePrompt{Message: "code"}, "manual_code"}, {AuthSelectPrompt{Message: "select", Options: []AuthSelectOption{{ID: "id", Label: "label"}}}, "select"},
		{AuthInfoEvent{Message: "info", Links: []AuthInfoLink{{URL: "https://example.test", Label: "docs"}}}, "info"}, {AuthURLEvent{URL: "https://example.test"}, "auth_url"}, {AuthDeviceCodeEvent{UserCode: "code", VerificationURI: "https://example.test", IntervalSeconds: new(5.0), ExpiresInSeconds: new(60.0)}, "device_code"}, {AuthProgressEvent{Message: "progress"}, "progress"},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		var object map[string]any
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		if object["type"] != tc.kind {
			t.Fatalf("discriminant lost: %s", raw)
		}
	}
}

func TestProviderLoginAdditionalBranches(t *testing.T) {
	credential, _ := loginWithAnswers(t, bedrockAuth(), []string{"credential-chain", ""})
	if credential.Type != CredentialAPIKey || credential.Key != "" || credential.Env != nil {
		t.Fatalf("chain=%#v", credential)
	}
	for _, path := range []string{"/credentials/service.json", ""} {
		credential, _ := loginWithAnswers(t, vertexAuth(), []string{"service-account", "project", "location", path})
		if credential.Env["GOOGLE_APPLICATION_CREDENTIALS"] != path {
			t.Fatalf("service account=%#v", credential)
		}
		if _, present := credential.Env["GOOGLE_APPLICATION_CREDENTIALS"]; present != (path != "") {
			t.Fatal("empty path is not omitted")
		}
	}
}
