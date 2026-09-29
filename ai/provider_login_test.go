package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

// Pi 0.87.1 providers own the complete prompt sequence and store replies verbatim:
// amazon-bedrock.ts:13-52, google-vertex.ts:15-63, cloudflare-auth.ts:57-90,
// anthropic.ts:12-18, and auth/helpers.ts:envApiKeyAuth.
func TestBuiltinProviderLoginMatchesPi(t *testing.T) {
	cases := []struct {
		provider string
		answers  []string
	}{
		{"amazon-bedrock", []string{"bearer-token", "  token  "}},
		{"amazon-bedrock", []string{"aws-profile", "team"}},
		{"amazon-bedrock", []string{"credential-chain", ""}},
		{"google-vertex", []string{"api-key", "  key  "}},
		{"google-vertex", []string{"adc", "project", "region"}},
		{"google-vertex", []string{"service-account", "project", "region", "~/account.json"}},
		{"google-vertex", []string{"service-account", "project", "region", ""}},
		{"cloudflare-ai-gateway", []string{" key ", "account", "gateway"}},
		{"cloudflare-workers-ai", []string{" key ", "account"}},
		{"anthropic", []string{"  key  "}},
		{"openai", []string{""}},
		{"openrouter", []string{"  key  "}},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.answers[0], func(t *testing.T) {
			input, err := json.Marshal(map[string]any{"provider": tc.provider, "answers": tc.answers})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), "node", "ai/testdata/provider-login-pi.mjs")
			cmd.Dir = ".."
			cmd.Stdin = bytes.NewReader(input)
			oracle, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Pi probe: %v\n%s", err, oracle)
			}
			var want any
			if err := json.Unmarshal(oracle, &want); err != nil {
				t.Fatalf("Pi JSON: %v\n%s", err, oracle)
			}
			auth, err := BuiltinProviderAuth(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if auth.APIKey.Login == nil {
				t.Fatal("provider-owned login missing")
			}
			events := []any{}
			index := 0
			cred, err := auth.APIKey.Login(t.Context(), AuthInteraction{
				Prompt: func(_ context.Context, p AuthPrompt) (string, error) {
					events = append(events, loginPromptJSON(p))
					if index >= len(tc.answers) {
						return "", errors.New("unexpected prompt")
					}
					answer := tc.answers[index]
					index++
					return answer, nil
				}, Notify: func(e AuthEvent) {
					if info, ok := e.(AuthInfoEvent); ok {
						links := []any{}
						for _, link := range info.Links {
							links = append(links, map[string]any{"label": link.Label, "url": link.URL})
						}
						events = append(events, map[string]any{"type": "info", "message": info.Message, "links": links})
					} else {
						t.Errorf("unexpected event %T", e)
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]any{"events": events, "credential": cred})
			if err != nil {
				t.Fatal(err)
			}
			var got any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Pi: %s\nGo: %s", oracle, raw)
			}
		})
	}
}

func loginPromptJSON(p AuthPrompt) any {
	result := map[string]any{}
	switch prompt := p.(type) {
	case AuthTextPrompt:
		result["type"] = "text"
		result["message"] = prompt.Message
	case AuthSecretPrompt:
		result["type"] = "secret"
		result["message"] = prompt.Message
	case AuthSelectPrompt:
		result["type"] = "select"
		result["message"] = prompt.Message
		options := []any{}
		for _, option := range prompt.Options {
			options = append(options, map[string]any{"id": option.ID, "label": option.Label})
		}
		result["options"] = options
	case AuthManualCodePrompt:
		result["type"] = "manual_code"
		result["message"] = prompt.Message
	}
	return result
}

func TestBuiltinProviderLoginCancellationNeverStoresPartialCredential(t *testing.T) {
	for _, id := range []string{"amazon-bedrock", "google-vertex", "cloudflare-ai-gateway", "cloudflare-workers-ai", "anthropic", "openai"} {
		t.Run(id, func(t *testing.T) {
			auth, err := BuiltinProviderAuth(id)
			if err != nil {
				t.Fatal(err)
			}
			if auth.APIKey.Login == nil {
				t.Fatal("provider-owned login missing")
			}
			credentials := NewInMemoryCredentialStore()
			models := CreateModels(CreateModelsOptions{Credentials: credentials})
			models.SetProvider(&ModelsProvider{ID: id, Name: id, Auth: auth})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err = models.Login(ctx, id, CredentialAPIKey, AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) { cancel(); return "", context.Canceled }, Notify: func(AuthEvent) {}})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
			models.operations.Wait()
			credentials.operations.Wait()
			stored, err := credentials.Read(t.Context(), id)
			if err != nil || stored != nil {
				t.Fatalf("stored=%v err=%v", stored, err)
			}
		})
	}
}
