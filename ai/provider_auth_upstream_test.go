package ai

// pi: packages/ai/src/providers/amazon-bedrock.ts
// pi: packages/ai/src/providers/google-vertex.ts
// pi: packages/ai/src/providers/cloudflare-auth.ts

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func providerTestAuthContext(env map[string]string, files ...string) AuthContext {
	return AuthContext{Env: func(name string) (string, bool) { value, ok := env[name]; return value, ok }, FileExists: func(path string) bool { return slices.Contains(files, path) }}
}

// Ports packages/ai/test/providers.test.ts:293-326,387-429 with the same login answers, credentials, information-link labels and stored-env resolution.
func TestProvidersOwnedCloudLoginUpstream(t *testing.T) {
	cases := []struct {
		provider string
		answers  []string
		want     Credential
		info     string
	}{
		{"amazon-bedrock", []string{"bearer-token", "bedrock-token"}, Credential{Type: CredentialAPIKey, Key: "bedrock-token"}, ""},
		{"amazon-bedrock", []string{"aws-profile", "work"}, Credential{Type: CredentialAPIKey, Env: map[string]string{"AWS_PROFILE": "work"}}, "AWS credential provider chain"},
		{"google-vertex", []string{"api-key", "vertex-key"}, Credential{Type: CredentialAPIKey, Key: "vertex-key"}, ""},
		{"google-vertex", []string{"adc", "project-id", "us-central1"}, Credential{Type: CredentialAPIKey, Env: map[string]string{"GOOGLE_CLOUD_PROJECT": "project-id", "GOOGLE_CLOUD_LOCATION": "us-central1"}}, "Application Default Credentials"},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.answers[0], func(t *testing.T) {
			auth, err := BuiltinProviderAuth(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			var events []AuthEvent
			index := 0
			credential, err := auth.APIKey.Login(t.Context(), AuthInteraction{Prompt: func(context.Context, AuthPrompt) (string, error) {
				answer := tc.answers[index]
				index++
				return answer, nil
			}, Notify: func(event AuthEvent) { events = append(events, event) }})
			if err != nil || !reflect.DeepEqual(credential, tc.want) {
				t.Fatalf("credential=%+v err=%v want=%+v", credential, err, tc.want)
			}
			if tc.info != "" {
				if len(events) != 1 {
					t.Fatalf("events=%+v", events)
				}
				info, ok := events[0].(AuthInfoEvent)
				if !ok || len(info.Links) != 1 || info.Links[0].Label != tc.info {
					t.Fatalf("information=%+v", events)
				}
				resolved, err := auth.APIKey.Resolve(t.Context(), APIKeyAuthInput{Credential: &credential, Ctx: providerTestAuthContext(nil, "~/.config/gcloud/application_default_credentials.json")})
				if err != nil || resolved == nil || !reflect.DeepEqual(resolved.Auth, ModelAuth{}) || !reflect.DeepEqual(resolved.Env, tc.want.Env) {
					t.Fatalf("resolved=%+v err=%v", resolved, err)
				}
			}
		})
	}
}

// Ports packages/ai/test/providers.test.ts:328-385,431-451.
func TestProvidersCloudEnvironmentAuthUpstream(t *testing.T) {
	cases := []struct {
		name, provider string
		env            map[string]string
		files          []string
		ready          bool
		auth           ModelAuth
		scoped         map[string]string
		source         string
		exactSource    bool // Pi asserts toBe for Bedrock and toContain for Vertex
	}{
		{name: "bedrock ambient", provider: "amazon-bedrock", env: map[string]string{"AWS_PROFILE": "dev"}, ready: true, source: "AWS_PROFILE", exactSource: true},
		{name: "bedrock unconfigured", provider: "amazon-bedrock"},
		{name: "workers missing account", provider: "cloudflare-workers-ai", env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key"}},
		{name: "workers configured", provider: "cloudflare-workers-ai", env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id"}, ready: true, auth: ModelAuth{APIKey: "cf-key"}, scoped: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "account-id"}},
		{name: "gateway missing gateway", provider: "cloudflare-ai-gateway", env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id"}},
		{name: "gateway configured", provider: "cloudflare-ai-gateway", env: map[string]string{"CLOUDFLARE_API_KEY": "cf-key", "CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"}, ready: true, auth: ModelAuth{Headers: ProviderHeaders{"cf-aig-authorization": new("Bearer cf-key"), "Authorization": nil, "x-api-key": nil}}, scoped: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "account-id", "CLOUDFLARE_GATEWAY_ID": "gateway-id"}},
		{name: "vertex ADC", provider: "google-vertex", env: map[string]string{"GOOGLE_CLOUD_PROJECT": "proj", "GOOGLE_CLOUD_LOCATION": "us-central1"}, files: []string{"~/.config/gcloud/application_default_credentials.json"}, ready: true, source: "application default"},
		{name: "vertex partial ADC", provider: "google-vertex", env: map[string]string{"GOOGLE_CLOUD_PROJECT": "proj"}, files: []string{"~/.config/gcloud/application_default_credentials.json"}},
		{name: "vertex key", provider: "google-vertex", env: map[string]string{"GOOGLE_CLOUD_API_KEY": "vertex-key"}, ready: true, auth: ModelAuth{APIKey: "vertex-key"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := BuiltinProviderAuth(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			models := CreateModels(CreateModelsOptions{AuthContext: new(providerTestAuthContext(tc.env, tc.files...))})
			models.SetProvider(&ModelsProvider{ID: tc.provider, Auth: auth})
			result, err := models.GetAuth(t.Context(), tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.ready {
				if result != nil {
					t.Fatalf("unconfigured result=%+v", result)
				}
				return
			}
			if result == nil || !reflect.DeepEqual(result.Auth, tc.auth) || !reflect.DeepEqual(result.Env, tc.scoped) || tc.source != "" && !strings.Contains(result.Source, tc.source) || tc.exactSource && result.Source != tc.source {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
