package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestMergeHeadersSupportsCaseInsensitiveDeletionMarkers(t *testing.T) {
	base := map[string]string{"Authorization": "Bearer old", "X-Trace": "base"}
	deleteHeader := (*string)(nil)
	overrideValue := "override"
	overrides := map[string]*string{
		"authorization": deleteHeader,
		"x-trace":       &overrideValue,
	}
	got := mergeHeaders(base, overrides, nil)
	if _, exists := got["Authorization"]; exists {
		t.Fatalf("deleted Authorization header remained: %v", got)
	}
	if _, exists := got["authorization"]; exists {
		t.Fatalf("nil deletion marker was emitted: %v", got)
	}
	if got["x-trace"] != "override" || len(got) != 1 {
		t.Fatalf("merged headers = %v, want only x-trace=override", got)
	}
}

func TestModelRegistrySamplingParamsMergePerKey(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"custom":{"baseUrl":"https://example.test","models":[{"id":"model","samplingParams":{"top_p":0.9,"min_p":0.1}}],"modelOverrides":{"model":{"samplingParams":{"top_p":0.8,"repetition_penalty":1.1}}}}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, ok := NewModelRegistry(dir).Resolve("custom", "model")
	if !ok {
		t.Fatal("custom/model was not resolved")
	}
	if entry.SamplingParams["top_p"] != 0.8 || entry.SamplingParams["min_p"] != 0.1 || entry.SamplingParams["repetition_penalty"] != 1.1 {
		t.Fatalf("sampling params = %v", entry.SamplingParams)
	}
}

func TestModelRegistryNullableHeadersDeleteProviderDefaults(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"custom":{"baseUrl":"https://example.test","headers":{"Authorization":"Bearer old","X-Trace":"base"},"models":[{"id":"model","headers":{"authorization":null,"x-trace":"override"}}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	entry, ok := registry.Resolve("custom", "model")
	if !ok {
		t.Fatal("custom/model was not resolved")
	}
	if len(entry.Headers) != 1 || entry.Headers["x-trace"] != "override" {
		t.Fatalf("resolved headers = %v, want only x-trace=override", entry.Headers)
	}
}

func TestModelRegistry_RefreshReloadsModelsJSON(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	_ = os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://a.test","models":[{"id":"m1","name":"M1"}]}}}`), 0o644)

	r := NewModelRegistry(dir)
	entry, ok := r.Resolve("custom", "m1")
	if !ok || entry.BaseURL != "https://a.test" {
		t.Fatalf("initial resolve failed: ok=%v entry=%+v", ok, entry)
	}

	// Change models.json and refresh.
	_ = os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://b.test","models":[{"id":"m1","name":"M1v2"}]}}}`), 0o644)
	r.Refresh()

	entry, ok = r.Resolve("custom", "m1")
	if !ok || entry.BaseURL != "https://b.test" {
		t.Fatalf("after refresh: ok=%v entry=%+v", ok, entry)
	}
}

func TestModelRegistry_RefreshContextCancellationDoesNotPublish(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://a.test","models":[{"id":"m1"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewModelRegistry(dir)
	if err := os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://b.test","models":[{"id":"m1"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := r.refreshContext(ctx)
	if !result.Aborted {
		t.Fatal("cancelled refresh did not report aborted")
	}
	entry, ok := r.Resolve("custom", "m1")
	if !ok || entry.BaseURL != "https://a.test" {
		t.Fatalf("cancelled refresh published new config: ok=%v entry=%+v", ok, entry)
	}
}

func TestModelRegistry_RefreshPreservesDynamicProviders(t *testing.T) {
	dir := t.TempDir()
	r := NewModelRegistry(dir)
	r.RegisterProvider("ext-prov", extension.ProviderConfig{
		BaseURL: "https://ext.test",
		Models: []extension.ProviderModelConfig{
			{ID: "ext-m1", Name: "Ext M1"},
		},
	})
	r.Refresh()
	entry, ok := r.Resolve("ext-prov", "ext-m1")
	if !ok || entry.BaseURL != "https://ext.test" {
		t.Fatalf("dynamic provider lost after Refresh: ok=%v entry=%+v", ok, entry)
	}
}

func TestModelRegistry_HasConfiguredAuth_EnvKey(t *testing.T) {
	dir := t.TempDir()
	r := NewModelRegistry(dir)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	if !r.HasConfiguredAuth("openai") {
		t.Fatal("expected HasConfiguredAuth=true for openai with env key")
	}
	if r.HasConfiguredAuth("unknown-provider") {
		t.Fatal("expected HasConfiguredAuth=false for unknown provider")
	}
}

func TestModelRegistry_TogetherProviderEnvAndDisplayName(t *testing.T) {
	t.Setenv("TOGETHER_API_KEY", "sk-together")
	r := NewModelRegistry(t.TempDir())
	entry, ok := r.Resolve("together", "moonshotai/Kimi-K2.6")
	if !ok {
		t.Fatal("Resolve returned ok=false for together")
	}
	if entry.APIKey != "sk-together" {
		t.Fatalf("APIKey = %q, want sk-together", entry.APIKey)
	}
	if !isBuiltInProvider("together") {
		t.Fatal("together should be treated as a built-in provider")
	}
	if got := r.GetProviderDisplayName("together"); got != "Together AI" {
		t.Fatalf("display name = %q, want Together AI", got)
	}
	if count := r.AvailableProviderCount(); count < 1 {
		t.Fatalf("expected together to count as available, got %d", count)
	}
}

func TestModelRegistry_ResolveAPIKeyFromEnv_AdditionalProviders(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-deepseek")
	t.Setenv("MOONSHOT_API_KEY", "sk-moonshot")
	t.Setenv("CLOUDFLARE_API_KEY", "sk-cloudflare")
	t.Setenv("XIAOMI_API_KEY", "sk-xiaomi")
	t.Setenv("TOGETHER_API_KEY", "sk-together")

	cases := []struct {
		provider string
		want     string
	}{
		{provider: "deepseek", want: "sk-deepseek"},
		{provider: "moonshotai", want: "sk-moonshot"},
		{provider: "moonshotai-cn", want: "sk-moonshot"},
		{provider: "cloudflare-workers-ai", want: "sk-cloudflare"},
		{provider: "cloudflare-ai-gateway", want: "sk-cloudflare"},
		{provider: "xiaomi", want: "sk-xiaomi"},
		{provider: "together", want: "sk-together"},
	}
	for _, tc := range cases {
		if got := resolveAPIKeyFromEnv(tc.provider); got != tc.want {
			t.Fatalf("resolveAPIKeyFromEnv(%q) = %q, want %q", tc.provider, got, tc.want)
		}
	}
}

func TestModelRegistry_HasConfiguredAuth_OAuth(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(authPath, []byte(`{"github-copilot":{"type":"oauth","access":"tok"}}`), 0o644)
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	r := NewModelRegistry(dir)
	r.SetAuthStorage(auth)

	if !r.HasConfiguredAuth("github-copilot") {
		t.Fatal("expected HasConfiguredAuth=true for github-copilot with stored oauth")
	}
}

func TestModelRegistry_RegisterProvider_InsecureThreadsToEntry(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	r.RegisterProvider("corp-ai", extension.ProviderConfig{
		BaseURL:  "https://corp.test/v1",
		APIKey:   "corp-key",
		API:      "openai-completions",
		Insecure: true,
		Models:   []extension.ProviderModelConfig{{ID: "corp-m1", Name: "Corp M1"}},
	})

	entry, ok := r.Resolve("corp-ai", "corp-m1")
	if !ok {
		t.Fatal("Resolve(corp-ai/corp-m1) not found")
	}
	if !entry.Insecure {
		t.Fatal("ModelEntry.Insecure = false, want true for an insecure-registered provider")
	}

	// Provider-level fallback (unnamed model under the provider) also carries it.
	defaults, ok := r.Resolve("corp-ai", "unnamed")
	if !ok {
		t.Fatal("Resolve(corp-ai/unnamed) not found")
	}
	if !defaults.Insecure {
		t.Fatal("provider-default ModelEntry.Insecure = false, want true")
	}

	// A provider registered without the flag must stay secure.
	r.RegisterProvider("safe-ai", extension.ProviderConfig{
		BaseURL: "https://safe.test/v1",
		APIKey:  "safe-key",
		Models:  []extension.ProviderModelConfig{{ID: "safe-m1", Name: "Safe M1"}},
	})
	safe, ok := r.Resolve("safe-ai", "safe-m1")
	if !ok {
		t.Fatal("Resolve(safe-ai/safe-m1) not found")
	}
	if safe.Insecure {
		t.Fatal("ModelEntry.Insecure = true for a provider registered without the flag")
	}
}

func TestModelRegistry_RegisterProvider_OAuthDetection(t *testing.T) {
	dir := t.TempDir()
	r := NewModelRegistry(dir)
	r.RegisterProvider("corp-ai", extension.ProviderConfig{
		BaseURL: "https://corp.test",
		OAuth:   &extension.ProviderOAuth{Name: "Corp AI SSO"},
		Models: []extension.ProviderModelConfig{
			{ID: "corp-m1", Name: "Corp M1"},
		},
	})

	// Without auth storage, HasConfiguredAuth should be false for oauth-only provider.
	if r.HasConfiguredAuth("corp-ai") {
		t.Fatal("expected HasConfiguredAuth=false without stored credentials")
	}

	// Wire auth with stored credentials.
	authPath := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(authPath, []byte(`{"corp-ai":{"type":"oauth","access":"tok"}}`), 0o644)
	auth, _ := ai.NewAuthStorage(authPath)
	r.SetAuthStorage(auth)

	if !r.HasConfiguredAuth("corp-ai") {
		t.Fatal("expected HasConfiguredAuth=true after wiring auth with stored oauth creds")
	}
}

func TestModelRegistry_GetAvailable(t *testing.T) {
	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(authPath, []byte(`{"authed-prov":{"type":"oauth","access":"tok"}}`), 0o644)
	auth, _ := ai.NewAuthStorage(authPath)

	r := NewModelRegistry(dir)
	r.SetAuthStorage(auth)

	// Register two providers: one with auth, one without.
	r.RegisterProvider("authed-prov", extension.ProviderConfig{
		BaseURL: "https://authed.test",
		OAuth:   &extension.ProviderOAuth{Name: "Authed"},
		Models: []extension.ProviderModelConfig{
			{ID: "m1", Name: "M1"},
		},
	})
	r.RegisterProvider("no-auth-prov", extension.ProviderConfig{
		BaseURL: "https://noauth.test",
		OAuth:   &extension.ProviderOAuth{Name: "NoAuth"},
		Models: []extension.ProviderModelConfig{
			{ID: "m2", Name: "M2"},
		},
	})

	available := r.GetAvailable()
	if len(available) != 1 {
		t.Fatalf("expected 1 available model, got %d", len(available))
	}
	if available[0].ModelID != "m1" {
		t.Fatalf("expected m1, got %s", available[0].ModelID)
	}
}

func TestModelRegistry_AvailableProviderCount(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	r := NewModelRegistry(dir)
	count := r.AvailableProviderCount()
	if count < 2 {
		t.Fatalf("expected at least 2 providers with env keys, got %d", count)
	}
}

func TestModelRegistry_GetProviderDisplayName(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	if got := r.GetProviderDisplayName("deepseek"); got != "DeepSeek" {
		t.Fatalf("display name = %q, want DeepSeek", got)
	}
	r.RegisterProvider("corp-ai", extension.ProviderConfig{Name: "Corp AI"})
	if got := r.GetProviderDisplayName("corp-ai"); got != "Corp AI" {
		t.Fatalf("display name = %q, want Corp AI", got)
	}
}

// Provider labels added upstream in 0.80.x (provider-display-names.js). These
// providers ship selectable models in the generated catalog, so an unmapped
// label surfaces the raw provider ID in the model selector.
func TestModelRegistry_GetProviderDisplayName_Upstream0803Labels(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	want := map[string]string{
		"zai":                   "ZAI Coding Plan (Global)",
		"zai-coding-cn":         "ZAI Coding Plan (China)",
		"ant-ling":              "Ant Ling",
		"xiaomi-token-plan-cn":  "Xiaomi MiMo Token Plan (China)",
		"xiaomi-token-plan-ams": "Xiaomi MiMo Token Plan (Amsterdam)",
		"xiaomi-token-plan-sgp": "Xiaomi MiMo Token Plan (Singapore)",
	}
	for id, label := range want {
		if got := r.GetProviderDisplayName(id); got != label {
			t.Errorf("GetProviderDisplayName(%q) = %q, want %q", id, got, label)
		}
	}
}

func TestModelRegistry_GetProviderAuthStatus_DynamicEnvLabel(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	t.Setenv("CORP_AI_KEY", "secret")
	r.RegisterProvider("corp-ai", extension.ProviderConfig{APIKey: "$CORP_AI_KEY"})
	status := r.GetProviderAuthStatus("corp-ai")
	if !status.Configured || status.Source != ai.AuthSourceEnvironment || status.Label != "CORP_AI_KEY" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestModelRegistry_ModelOverridesMergeThinkingLevelMap(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	high := "HIGH"
	off := "disabled"
	low := "LOW"
	r.RegisterProvider("corp-ai", extension.ProviderConfig{
		BaseURL: "https://corp.example/v1",
		Models: []extension.ProviderModelConfig{{
			ID:               "model-1",
			Name:             "Model 1",
			Reasoning:        true,
			ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingHigh: &high, ai.ThinkingOff: &off},
		}},
	})
	r.RegisterProvider("corp-ai", extension.ProviderConfig{
		BaseURL: "https://corp.example/v1",
	})
	r.mu.Lock()
	r.upsertRegisteredProviderLocked("corp-ai", providerConfig{
		ModelOverrides: map[string]modelOverrideJSON{
			"model-1": {
				ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingOff: nil, ai.ThinkingLow: &low},
			},
		},
	})
	r.mu.Unlock()

	entry, ok := r.Resolve("corp-ai", "model-1")
	if !ok {
		t.Fatal("Resolve returned ok=false")
	}
	if entry.ThinkingLevelMap[ai.ThinkingHigh] == nil || *entry.ThinkingLevelMap[ai.ThinkingHigh] != high {
		t.Fatalf("high mapping = %v, want %q", entry.ThinkingLevelMap[ai.ThinkingHigh], high)
	}
	if _, ok := entry.ThinkingLevelMap[ai.ThinkingOff]; !ok || entry.ThinkingLevelMap[ai.ThinkingOff] != nil {
		t.Fatalf("off mapping = %v, want explicit nil", entry.ThinkingLevelMap[ai.ThinkingOff])
	}
	if entry.ThinkingLevelMap[ai.ThinkingLow] == nil || *entry.ThinkingLevelMap[ai.ThinkingLow] != low {
		t.Fatalf("low mapping = %v, want %q", entry.ThinkingLevelMap[ai.ThinkingLow], low)
	}
}

func TestModelRegistry_CustomModelBaseURLOverride(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	data := `{"providers":{"custom":{"baseUrl":"https://provider.example/v1","models":[{"id":"m1","name":"M1","baseUrl":"https://model.example/v1","reasoning":true,"thinkingLevelMap":{"off":null,"high":"HIGH"}}]}}}`
	if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewModelRegistry(dir)
	entry, ok := r.Resolve("custom", "m1")
	if !ok {
		t.Fatal("Resolve returned ok=false")
	}
	if entry.BaseURL != "https://model.example/v1" {
		t.Fatalf("BaseURL = %q, want model override", entry.BaseURL)
	}
	if _, ok := entry.ThinkingLevelMap[ai.ThinkingOff]; !ok || entry.ThinkingLevelMap[ai.ThinkingOff] != nil {
		t.Fatalf("off mapping = %v, want explicit nil", entry.ThinkingLevelMap[ai.ThinkingOff])
	}
	if entry.ThinkingLevelMap[ai.ThinkingHigh] == nil || *entry.ThinkingLevelMap[ai.ThinkingHigh] != "HIGH" {
		t.Fatalf("high mapping = %v, want HIGH", entry.ThinkingLevelMap[ai.ThinkingHigh])
	}
}

func TestModelRegistry_GetAvailable_SortedStableForSameNameFamily(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	r.RegisterProvider("z-prov", extension.ProviderConfig{BaseURL: "https://z.example", APIKey: "Z_API_KEY", Models: []extension.ProviderModelConfig{{ID: "z-1", Name: "z-1"}}})
	r.RegisterProvider("a-prov", extension.ProviderConfig{BaseURL: "https://a.example", APIKey: "A_API_KEY", Models: []extension.ProviderModelConfig{{ID: "a-1", Name: "a-1"}}})
	t.Setenv("Z_API_KEY", "z")
	t.Setenv("A_API_KEY", "a")
	got := r.GetAvailable()
	names := make([]string, 0, len(got))
	for _, entry := range got {
		names = append(names, entry.ProviderID)
	}
	if !slices.Contains(names, "a-prov") || !slices.Contains(names, "z-prov") {
		t.Fatalf("GetAvailable providers = %v", names)
	}
}

// TestModelRegistry_GetAvailable_IncludesModelsJSONProviders proves that a
// provider defined purely in models.json (no extension RegisterProvider call)
// surfaces through GetAvailable() the same as a dynamically registered one.
// GetAvailable feeds --list-models and the interactive /model, Ctrl+P, and
// /scoped-models pickers (interactive.go dynamicProviderModels ->
// ModelRegistry.GetAvailable), so a models.json-only provider was previously
// resolvable via --model provider/id (Resolve checks r.config.Providers) but
// invisible in every listing surface. Mirrors upstream model-registry.ts
// getAvailable(), which filters a single #models list merging built-ins,
// models.json custom overlays, and runtime extension overlays -- not just
// extension-registered providers.
func TestModelRegistry_GetAvailable_IncludesModelsJSONProviders(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	data := `{"providers":{
		"glm-xd670":{"baseUrl":"http://xd670.example/v1","api":"openai-completions","apiKey":"not-needed","models":[{"id":"glm-5.2-fp8","name":"GLM-5.2-FP8"}]},
		"no-auth-prov":{"baseUrl":"https://noauth.example/v1","models":[{"id":"m1","name":"M1"}]}
	}}`
	if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewModelRegistry(dir)
	available := r.GetAvailable()

	var found *ModelEntry
	for i := range available {
		if available[i].ProviderID == "glm-xd670" && available[i].ModelID == "glm-5.2-fp8" {
			found = &available[i]
		}
		if available[i].ProviderID == "no-auth-prov" {
			t.Errorf("no-auth-prov has no apiKey/env configured and should not be available, got %+v", available[i])
		}
	}
	if found == nil {
		t.Fatalf("expected glm-xd670/glm-5.2-fp8 in GetAvailable(), got %+v", available)
	}
	if found.BaseURL != "http://xd670.example/v1" {
		t.Errorf("BaseURL = %q, want http://xd670.example/v1", found.BaseURL)
	}
}

// TestModelRegistry_GetAvailable_DynamicProviderTakesPrecedenceOverModelsJSON
// proves that when a provider name is both extension-registered and present
// in models.json, GetAvailable() reports the dynamic registration exactly
// once -- matching Resolve()'s precedence (r.dynamic checked before
// r.config.Providers) -- rather than double-listing or preferring the stale
// on-disk config.
func TestModelRegistry_GetAvailable_DynamicProviderTakesPrecedenceOverModelsJSON(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	data := `{"providers":{"shared":{"baseUrl":"https://stale.example/v1","apiKey":"stale-key","models":[{"id":"m1","name":"Stale M1"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewModelRegistry(dir)
	r.RegisterProvider("shared", extension.ProviderConfig{
		BaseURL: "https://fresh.example/v1",
		APIKey:  "fresh-key",
		Models:  []extension.ProviderModelConfig{{ID: "m1", Name: "Fresh M1"}},
	})

	available := r.GetAvailable()
	count := 0
	for _, e := range available {
		if e.ProviderID == "shared" && e.ModelID == "m1" {
			count++
			if e.BaseURL != "https://fresh.example/v1" {
				t.Errorf("BaseURL = %q, want dynamic registration's https://fresh.example/v1", e.BaseURL)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected shared/m1 exactly once, got %d in %+v", count, available)
	}
}

// TestModelRegistry_ResolveAPIKeyUsesProviderEnv proves the production wiring:
// a models.json provider whose apiKey is a "$VAR" reference resolves against
// the provider-scoped env stored in auth.json (precedence over process env).
// Mirrors upstream model-registry.ts getApiKeyAndHeaders threading
// authStorage.getProviderEnv(provider) into resolveConfigValue.
func TestModelRegistry_ResolveAPIKeyUsesProviderEnv(t *testing.T) {
	t.Setenv("MYPROV_KEY", "from-process")
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	_ = os.WriteFile(modelsPath, []byte(`{"providers":{"myprov":{"baseUrl":"https://a.test","apiKey":"$MYPROV_KEY","models":[{"id":"m1","name":"M1"}]}}}`), 0o644)

	authPath := filepath.Join(dir, "auth.json")
	_ = os.WriteFile(authPath, []byte(`{"myprov":{"type":"api_key","key":"unused","env":{"MYPROV_KEY":"from-scope"}}}`), 0o644)
	auth, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}

	r := NewModelRegistry(dir)
	r.SetAuthStorage(auth)

	entry, ok := r.Resolve("myprov", "m1")
	if !ok {
		t.Fatal("Resolve returned ok=false")
	}
	if entry.APIKey != "from-scope" {
		t.Errorf("APIKey should resolve against provider-scoped env: got %q want %q", entry.APIKey, "from-scope")
	}

	// Without authStorage, the same provider falls back to the process env.
	r2 := NewModelRegistry(dir)
	entry2, ok := r2.Resolve("myprov", "m1")
	if !ok || entry2.APIKey != "from-process" {
		t.Errorf("without provider env, APIKey should use process env: ok=%v got %q", ok, entry2.APIKey)
	}
}

func TestModelRegistryEnvDiscoveryUsesCurrentProviderTable(t *testing.T) {
	t.Setenv("COPILOT_GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "generic-github-token")
	t.Setenv("GITHUB_TOKEN", "generic-github-token")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "bearer-only")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("QWEN_TOKEN_PLAN_API_KEY", "qwen-key")
	t.Setenv("META_API_KEY", "meta-key")
	r := NewModelRegistry(t.TempDir())
	for _, provider := range []string{"github-copilot", "anthropic"} {
		if got := resolveAPIKeyFromEnv(provider); got != "" {
			t.Fatalf("%s resolved a non-API-key token: %q", provider, got)
		}
	}
	for _, provider := range []string{"qwen-token-plan", "qwen-token-plan-individual", "meta"} {
		if !r.HasAnyKey(provider) {
			t.Errorf("%s did not discover its configured environment key", provider)
		}
	}
}
