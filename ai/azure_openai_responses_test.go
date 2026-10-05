package ai

import (
	"context"
	"testing"
)

func TestResolveAzureDeploymentName(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-4o=my-deployment, other = alt")
	if got := ResolveAzureDeploymentName("gpt-4o", AzureEndpointOptions{DeploymentName: "", Env: nil}); got != "my-deployment" {
		t.Fatalf("ResolveAzureDeploymentName = %q, want my-deployment", got)
	}
	if got := ResolveAzureDeploymentName("gpt-5", AzureEndpointOptions{DeploymentName: "explicit", Env: nil}); got != "explicit" {
		t.Fatalf("explicit deployment = %q, want explicit", got)
	}
	if got := ResolveAzureDeploymentName("unmapped", AzureEndpointOptions{DeploymentName: "", Env: nil}); got != "unmapped" {
		t.Fatalf("fallback deployment = %q, want unmapped", got)
	}
}

func TestResolveAzureBaseURL(t *testing.T) {
	if _, err := ResolveAzureBaseURL("", AzureEndpointOptions{}); err == nil {
		t.Fatal("expected error when no base URL or resource name configured")
	}
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "my-resource")
	got, err := ResolveAzureBaseURL("", AzureEndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://my-resource.openai.azure.com/openai/v1"
	if got != want {
		t.Fatalf("base URL = %q, want %q", got, want)
	}
	// The model's base URL is the last resort: the resource name outranks it (azure-openai-config.ts:resolveAzureBaseUrl).
	got, err = ResolveAzureBaseURL("https://example.test/openai/v1/", AzureEndpointOptions{})
	if err != nil || got != want {
		t.Fatalf("model base URL with a resource name = %q, %v; want the resource name's %q", got, err, want)
	}
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "")
	got, err = ResolveAzureBaseURL("https://example.test/openai/v1/", AzureEndpointOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.test/openai/v1" {
		t.Fatalf("trimmed base URL = %q", got)
	}

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "azure host root gains openai v1 path",
			in:   "https://my-resource.openai.azure.com",
			want: "https://my-resource.openai.azure.com/openai/v1",
		},
		{
			name: "azure host openai path gains version",
			in:   "https://my-resource.openai.azure.com/openai",
			want: "https://my-resource.openai.azure.com/openai/v1",
		},
		{
			name: "cognitive services host gains openai v1 path",
			in:   "https://my-resource.cognitiveservices.azure.com/",
			want: "https://my-resource.cognitiveservices.azure.com/openai/v1",
		},
		{
			name: "foundry ai host gains openai v1 path",
			in:   "https://my-project.services.ai.azure.com/",
			want: "https://my-project.services.ai.azure.com/openai/v1",
		},
		{
			name: "responses endpoint normalizes to azure base path",
			in:   "https://my-project.services.ai.azure.com/openai/v1/responses?api-version=preview",
			want: "https://my-project.services.ai.azure.com/openai/v1",
		},
		{
			name: "existing azure version path preserved",
			in:   "https://my-resource.openai.azure.com/openai/v1?api-version=v1",
			want: "https://my-resource.openai.azure.com/openai/v1?api-version=v1",
		},
		{
			name: "non azure host preserves query",
			in:   "https://example.test/openai/v1?api-version=v1",
			want: "https://example.test/openai/v1?api-version=v1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveAzureBaseURL(tc.in, AzureEndpointOptions{})
			if err != nil {
				t.Fatalf("ResolveAzureBaseURL(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ResolveAzureBaseURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	if _, err := ResolveAzureBaseURL("not a url", AzureEndpointOptions{}); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestNewAzureOpenAIResponsesProvider(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_KEY", "azure-key")
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://example.test/openai/v1")
	p := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{Model: "gpt-4o"})
	op, ok := p.(*openAIResponsesProvider)
	if !ok {
		t.Fatalf("provider type = %T, want *openAIResponsesProvider", p)
	}
	if op.cfg.ProviderID != string(APIAzureOpenAIResponses) {
		t.Fatalf("ProviderID = %q, want %q", op.cfg.ProviderID, APIAzureOpenAIResponses)
	}
	if op.cfg.Model != "gpt-4o" {
		t.Fatalf("Model = %q, want gpt-4o", op.cfg.Model)
	}
	if op.cfg.APIKeyHeader != "api-key" || op.cfg.APIKeyPrefix != "" {
		t.Fatalf("unexpected auth header config: %#v", op.cfg)
	}
	if key, err := op.cfg.GetAPIKey(context.Background()); err != nil || key != "azure-key" {
		t.Fatalf("GetAPIKey = %q, %v", key, err)
	}
	if baseURL, err := op.cfg.GetBaseURL(context.Background()); err != nil || baseURL != "https://example.test/openai/v1/responses?api-version=v1" {
		t.Fatalf("GetBaseURL = %q, %v", baseURL, err)
	}
}

// 0.79.5: AZURE_OPENAI_* reads honor a provider-scoped env (cfg.Env) ahead of
// the process environment. Mirrors azure-openai-responses.ts getProviderEnvValue.
func TestResolveAzureDeploymentName_ScopedEnvPrecedence(t *testing.T) {
	t.Setenv("AZURE_OPENAI_DEPLOYMENT_NAME_MAP", "gpt-4o=process-dep")
	env := ProviderEnv{"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": "gpt-4o=scoped-dep"}
	if got := ResolveAzureDeploymentName("gpt-4o", AzureEndpointOptions{DeploymentName: "", Env: env}); got != "scoped-dep" {
		t.Fatalf("scoped deployment = %q, want scoped-dep", got)
	}
	// Absent from scoped env: falls back to process env.
	if got := ResolveAzureDeploymentName("gpt-4o", AzureEndpointOptions{Env: ProviderEnv{}}); got != "process-dep" {
		t.Fatalf("fallback deployment = %q, want process-dep", got)
	}
}

func TestResolveAzureAPIVersion_ScopedEnvPrecedence(t *testing.T) {
	t.Setenv("AZURE_OPENAI_API_VERSION", "process-version")
	env := ProviderEnv{"AZURE_OPENAI_API_VERSION": "scoped-version"}
	if got := azureAPIVersionForTest("", env); got != "scoped-version" {
		t.Fatalf("scoped api version = %q, want scoped-version", got)
	}
	// Explicit config still wins over scoped env.
	if got := azureAPIVersionForTest("explicit-version", env); got != "explicit-version" {
		t.Fatalf("explicit api version = %q, want explicit-version", got)
	}
	// Absent from scoped env: falls back to process env.
	if got := azureAPIVersionForTest("", ProviderEnv{}); got != "process-version" {
		t.Fatalf("fallback api version = %q, want process-version", got)
	}
}

func TestResolveAzureBaseURL_ScopedEnvPrecedence(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://process.openai.azure.com/openai/v1")
	env := ProviderEnv{"AZURE_OPENAI_BASE_URL": "https://scoped.openai.azure.com/openai/v1"}
	got, err := ResolveAzureBaseURL("", AzureEndpointOptions{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://scoped.openai.azure.com/openai/v1" {
		t.Fatalf("scoped base URL = %q, want scoped host", got)
	}
}

func TestResolveAzureBaseURL_ScopedResourceName(t *testing.T) {
	t.Setenv("AZURE_OPENAI_RESOURCE_NAME", "process-res")
	env := ProviderEnv{"AZURE_OPENAI_RESOURCE_NAME": "scoped-res"}
	got, err := ResolveAzureBaseURL("", AzureEndpointOptions{Env: env})
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://scoped-res.openai.azure.com/openai/v1" {
		t.Fatalf("scoped resource base URL = %q, want scoped-res host", got)
	}
}

// TestNewAzureOpenAIResponsesProvider_ThreadsScopedEnv proves cfg.Env reaches the
// resolved base URL through the provider constructor (the caller-path wiring).
func TestNewAzureOpenAIResponsesProvider_ThreadsScopedEnv(t *testing.T) {
	t.Setenv("AZURE_OPENAI_BASE_URL", "https://process.openai.azure.com/openai/v1")
	p := NewAzureOpenAIResponsesProvider(AzureOpenAIResponsesConfig{
		Model: "gpt-4o",
		Env:   ProviderEnv{"AZURE_OPENAI_BASE_URL": "https://scoped.openai.azure.com/openai/v1"},
	})
	op := p.(*openAIResponsesProvider)
	baseURL, err := op.cfg.GetBaseURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "https://scoped.openai.azure.com/openai/v1/responses?api-version=v1"
	if baseURL != want {
		t.Fatalf("GetBaseURL = %q, want %q", baseURL, want)
	}
}

func azureAPIVersionForTest(explicit string, env ProviderEnv) string {
	config, err := ResolveAzureConfig("https://example.test", AzureEndpointOptions{APIVersion: explicit, Env: env})
	if err != nil {
		panic(err)
	}
	return config.APIVersion
}

// Pi's parseDeploymentNameMap (azure-openai-config.ts) splits each trimmed entry with split("=", 2), which keeps only the text before a
// second "=", fills a Map so a later entry for a model replaces an earlier one, and trims with String.prototype.trim, which also strips a
// byte order mark. A model mapped to an empty name falls back to the model ID. The expected values are the published pi-ai 1.0.3
// resolveDeploymentName's results for the same AZURE_OPENAI_DEPLOYMENT_NAME_MAP values.
func TestResolveAzureDeploymentNameParsesTheMapLikePi(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"m=a=b", "a"},
		{"m=a,m=b", "b"},
		{"m= ,m=x", "x"},
		{"m=x,m= ", "x"},
		{"m=x,m= =y", "m"},
		{" m = d ", "d"},
		{"m=d,", "d"},
		{"=d", "m"},
		{"m=", "m"},
		{"m\u00a0=\u00a0d", "d"},
		{"\ufeffm=d", "d"},
		{"other=z", "m"},
	} {
		if got := ResolveAzureDeploymentName("m", AzureEndpointOptions{Env: ProviderEnv{"AZURE_OPENAI_DEPLOYMENT_NAME_MAP": tc.value}}); got != tc.want {
			t.Errorf("AZURE_OPENAI_DEPLOYMENT_NAME_MAP=%q: deployment = %q, want %q", tc.value, got, tc.want)
		}
	}
}
