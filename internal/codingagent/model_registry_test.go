package codingagent

// pi: packages/coding-agent/src/core/model-registry.ts

// pi: packages/coding-agent/src/core/model-config.ts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	config := `{"providers":{"custom":{"baseUrl":"https://example.test","api":"openai-completions","models":[{"id":"model","samplingParams":{"top_p":0.9,"min_p":0.1}}],"modelOverrides":{"model":{"samplingParams":{"top_p":0.8,"repetition_penalty":1.1}}}}}}`
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

// model-config.ts HeadersSchema is Record<string, string>: a null header value in models.json is a schema error in Pi 1.1.0, so the
// null deletion marker only exists for extension-registered providers.
func TestModelRegistryRejectsNullHeaderValuesInModelsJSON(t *testing.T) {
	dir := t.TempDir()
	config := `{"providers":{"custom":{"baseUrl":"https://example.test","api":"openai-completions","headers":{"Authorization":"Bearer old","X-Trace":"base"},"models":[{"id":"model","headers":{"authorization":null,"x-trace":"override"}}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	want := "Invalid models.json schema:\n  - providers.custom.models.0.headers.authorization: must be string\n\nFile: " + filepath.Join(dir, "models.json")
	if got := registry.LoadError(); got != want {
		t.Fatalf("LoadError() = %q, want %q", got, want)
	}
	if _, ok := registry.Resolve("custom", "model"); ok {
		t.Fatal("a model of a models.json that fails the schema was resolved")
	}
}

func TestModelRegistry_RefreshReloadsModelsJSON(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	_ = os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://a.test","api":"openai-completions","models":[{"id":"m1","name":"M1"}]}}}`), 0o644)

	r := NewModelRegistry(dir)
	entry, ok := r.Resolve("custom", "m1")
	if !ok || entry.BaseURL != "https://a.test" {
		t.Fatalf("initial resolve failed: ok=%v entry=%+v", ok, entry)
	}

	// Change models.json and refresh.
	_ = os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://b.test","api":"openai-completions","models":[{"id":"m1","name":"M1v2"}]}}}`), 0o644)
	r.Refresh()

	entry, ok = r.Resolve("custom", "m1")
	if !ok || entry.BaseURL != "https://b.test" {
		t.Fatalf("after refresh: ok=%v entry=%+v", ok, entry)
	}
}

// Pi's refresh awaits ModelConfig.load, which takes no signal, and rebuilds the providers before it forwards the signal to the catalog refresh, so an aborted refresh still publishes models.json and only reports aborted (model-runtime.ts:700-712).
func TestModelRegistry_AbortedRefreshStillPublishesConfig(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://a.test","api":"openai-completions","models":[{"id":"m1"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := NewModelRegistry(dir)
	if err := os.WriteFile(modelsPath, []byte(`{"providers":{"custom":{"baseUrl":"https://b.test","api":"openai-completions","models":[{"id":"m1"},{"id":"m2"}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	notified := 0
	defer r.SetChangeListener(func() { notified++ })()
	result := r.refreshContext(ctx, nil)
	if !result.Aborted {
		t.Fatal("cancelled refresh did not report aborted")
	}
	for _, id := range []string{"m1", "m2"} {
		entry, ok := r.Resolve("custom", id)
		if !ok || entry.BaseURL != "https://b.test" {
			t.Fatalf("aborted refresh did not publish models.json for %s: ok=%v entry=%+v", id, ok, entry)
		}
	}
	if notified != 1 {
		t.Fatalf("listener notified %d times, want once for the published configuration", notified)
	}
}

func TestModelRegistry_RefreshPreservesDynamicProviders(t *testing.T) {
	dir := t.TempDir()
	r := NewModelRegistry(dir)
	if err := r.RegisterExtensionProvider("ext-prov", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://ext.test",
		Models: []extension.ProviderModelConfig{
			{ID: "ext-m1", Name: "Ext M1"},
		},
	}); err != nil {
		t.Error(err)
	}
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
	if len(ai.ListModels("together")) == 0 {
		t.Fatal("together should be treated as a built-in provider")
	}
	// Pi 0.99.2 model-registry.ts:176 getProviderDisplayName returns the runtime provider's `name` (providers/together.ts: "Together").
	if got := r.GetProviderDisplayName("together"); got != "Together" {
		t.Fatalf("display name = %q, want Together", got)
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

// D36: a registration with refreshModels is composed in the native collection (Pi provider-composer.ts:613), which must keep the provider's TLS
// opt-in; the models a refresh publishes resolve to entries that carry it, and a later registration without the flag clears it.
func TestModelRegistry_RegisterProvider_InsecureSurvivesNativeComposition(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	register := func(insecure bool) {
		t.Helper()
		if err := r.RegisterExtensionProvider("corp-refresh", extension.ProviderConfig{
			BaseURL:  "https://corp.test/v1",
			APIKey:   "corp-key",
			API:      "openai-completions",
			Insecure: insecure,
			Models:   []extension.ProviderModelConfig{{ID: "corp-m1", Name: "Corp M1"}},
			RefreshModels: func(extension.RefreshModelsContext) ([]extension.ProviderModelConfig, error) {
				return nil, nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	register(true)
	if !r.ProviderInsecure("corp-refresh") {
		t.Fatal("ProviderInsecure = false for a refreshModels registration that opted in")
	}
	register(false)
	if r.ProviderInsecure("corp-refresh") {
		t.Fatal("ProviderInsecure = true after a registration without the opt-in")
	}
}

func TestModelRegistry_RegisterProvider_InsecureThreadsToEntry(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	if err := r.RegisterExtensionProvider("corp-ai", extension.ProviderConfig{
		BaseURL:  "https://corp.test/v1",
		APIKey:   "corp-key",
		API:      "openai-completions",
		Insecure: true,
		Models:   []extension.ProviderModelConfig{{ID: "corp-m1", Name: "Corp M1"}},
	}); err != nil {
		t.Error(err)
	}

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
	if err := r.RegisterExtensionProvider("safe-ai", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://safe.test/v1",
		APIKey:  "safe-key",
		Models:  []extension.ProviderModelConfig{{ID: "safe-m1", Name: "Safe M1"}},
	}); err != nil {
		t.Error(err)
	}
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
	if err := r.RegisterExtensionProvider("corp-ai", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://corp.test",
		OAuth:   &extension.ProviderOAuth{Name: "Corp AI SSO"},
		Models: []extension.ProviderModelConfig{
			{ID: "corp-m1", Name: "Corp M1"},
		},
	}); err != nil {
		t.Error(err)
	}

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
	if err := r.RegisterExtensionProvider("authed-prov", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://authed.test",
		OAuth:   &extension.ProviderOAuth{Name: "Authed"},
		Models: []extension.ProviderModelConfig{
			{ID: "m1", Name: "M1"},
		},
	}); err != nil {
		t.Error(err)
	}
	if err := r.RegisterExtensionProvider("no-auth-prov", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://noauth.test",
		OAuth:   &extension.ProviderOAuth{Name: "NoAuth"},
		Models: []extension.ProviderModelConfig{
			{ID: "m2", Name: "M2"},
		},
	}); err != nil {
		t.Error(err)
	}

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
	if err := r.RegisterExtensionProvider("corp-ai", extension.ProviderConfig{Name: "Corp AI"}); err != nil {
		t.Error(err)
	}
	if got := r.GetProviderDisplayName("corp-ai"); got != "Corp AI" {
		t.Fatalf("display name = %q, want Corp AI", got)
	}
}

// Pi 0.99.2 model-registry.ts:176 getProviderDisplayName is `runtime.getProvider(provider)?.name ?? provider`, and every
// catalog provider's name is the `name` of its providers/<id>.ts (provider names and labels that ship selectable models
// in the generated catalog). The 0.80.x provider-display-names table no longer exists upstream.
func TestModelRegistry_GetProviderDisplayName_CatalogProviderNames(t *testing.T) {
	r := NewModelRegistry(t.TempDir())
	for _, id := range ai.GeneratedProviders {
		if got, want := r.GetProviderDisplayName(id), ai.ProviderDisplayName(id); got != want {
			t.Errorf("GetProviderDisplayName(%q) = %q, want the catalog provider name %q", id, got, want)
		}
	}
	want := map[string]string{
		"zai":                   "Z.AI",
		"zai-coding-cn":         "Z.AI Coding CN",
		"ant-ling":              "Ant Ling",
		"google":                "Google",
		"xiaomi-token-plan-cn":  "Xiaomi Token Plan CN",
		"xiaomi-token-plan-ams": "Xiaomi Token Plan AMS",
		"xiaomi-token-plan-sgp": "Xiaomi Token Plan SGP",
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
	if err := r.RegisterExtensionProvider("corp-ai", extension.ProviderConfig{APIKey: "$CORP_AI_KEY"}); err != nil {
		t.Error(err)
	}
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
	if err := r.RegisterExtensionProvider("corp-ai", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://corp.example/v1",
		Models: []extension.ProviderModelConfig{{
			ID:               "model-1",
			Name:             "Model 1",
			Reasoning:        true,
			ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingHigh: &high, ai.ThinkingOff: &off},
		}},
	}); err != nil {
		t.Error(err)
	}
	if err := r.RegisterExtensionProvider("corp-ai", extension.ProviderConfig{
		BaseURL: "https://corp.example/v1",
	}); err != nil {
		t.Error(err)
	}
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
	data := `{"providers":{"custom":{"baseUrl":"https://provider.example/v1","api":"openai-completions","models":[{"id":"m1","name":"M1","baseUrl":"https://model.example/v1","reasoning":true,"thinkingLevelMap":{"off":null,"high":"HIGH"}}]}}}`
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
	if err := r.RegisterExtensionProvider("z-prov", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://z.example", APIKey: "Z_API_KEY", Models: []extension.ProviderModelConfig{{ID: "z-1", Name: "z-1"}}}); err != nil {
		t.Error(err)
	}
	if err := r.RegisterExtensionProvider("a-prov", extension.ProviderConfig{API: ai.APIOpenAICompletions, BaseURL: "https://a.example", APIKey: "A_API_KEY", Models: []extension.ProviderModelConfig{{ID: "a-1", Name: "a-1"}}}); err != nil {
		t.Error(err)
	}
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
	data := `{"providers":{"shared":{"baseUrl":"https://stale.example/v1","api":"openai-completions","apiKey":"stale-key","models":[{"id":"m1","name":"Stale M1"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	r := NewModelRegistry(dir)
	if err := r.RegisterExtensionProvider("shared", extension.ProviderConfig{
		API:     ai.APIOpenAICompletions,
		BaseURL: "https://fresh.example/v1",
		APIKey:  "fresh-key",
		Models:  []extension.ProviderModelConfig{{ID: "m1", Name: "Fresh M1"}},
	}); err != nil {
		t.Error(err)
	}

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
	_ = os.WriteFile(modelsPath, []byte(`{"providers":{"myprov":{"baseUrl":"https://a.test","api":"openai-completions","apiKey":"$MYPROV_KEY","models":[{"id":"m1","name":"M1"}]}}}`), 0o644)

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

// Pi 1.0.2 extensionModelFromDefinition (provider-composer.ts:270-294) spreads the registered chat definition, so an extension model's samplingParamsByThinkingLevel (ProviderChatModelConfig, provider-composer.ts:72) reaches the composed model. The config is decoded from the registerProvider wire JSON, as the subprocess host decodes it.
func TestModelRegistryExtensionModelCarriesSamplingParamsByThinkingLevel(t *testing.T) {
	var config extension.ProviderConfig
	wire := `{"api":"openai-completions","baseUrl":"https://ext.test/v1","models":[{"id":"ext-model","name":"Ext","reasoning":true,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096,"samplingParams":{"top_p":0.5},"samplingParamsByThinkingLevel":{"off":{"temperature":0.1},"high":{"temperature":0.9,"top_k":7}}}]}`
	if err := json.Unmarshal([]byte(wire), &config); err != nil {
		t.Fatal(err)
	}
	r := NewModelRegistry(t.TempDir())
	if err := r.RegisterExtensionProvider("ext-sampling", config); err != nil {
		t.Fatal(err)
	}
	entry, ok := r.Resolve("ext-sampling", "ext-model")
	if !ok {
		t.Fatal("ext-sampling/ext-model was not resolved")
	}
	want := ai.SamplingParamsByThinkingLevel{ai.ThinkingOff: {"temperature": 0.1}, ai.ThinkingHigh: {"temperature": 0.9, "top_k": float64(7)}}
	if !reflect.DeepEqual(entry.SamplingParamsByThinkingLevel, want) || !reflect.DeepEqual(entry.SamplingParams, map[string]any{"top_p": 0.5}) {
		t.Fatalf("sampling = %v / %v, want %v / top_p 0.5", entry.SamplingParams, entry.SamplingParamsByThinkingLevel, want)
	}
}

// A resolved Model handed to a built-in API by an extension's streamSimple keeps its per-level sampling parameters (Pi 1.0.2 Model.samplingParamsByThinkingLevel, read by resolveSamplingParams).
func TestSubprocessAPIModelCarriesSamplingParamsByThinkingLevel(t *testing.T) {
	model, err := subprocessAPIModel(map[string]any{"id": "m", "name": "M", "provider": "p", "api": "openai-completions", "reasoning": true, "input": []any{"text"},
		"samplingParamsByThinkingLevel": map[string]any{"low": map[string]any{"temperature": 0.6}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (ai.SamplingParamsByThinkingLevel{ai.ThinkingLow: {"temperature": 0.6}}); !reflect.DeepEqual(model.SamplingParamsByThinkingLevel, want) {
		t.Fatalf("samplingParamsByThinkingLevel = %v, want %v", model.SamplingParamsByThinkingLevel, want)
	}
}

// TestModelRegistry_ExtensionRegistrationKeepsModelsJSONAuth proves that an
// extension registerProvider() call supplying only an api and a streamSimple
// callback does not hide a models.json provider. Upstream composes the
// registration over the models.json configuration field by field
// (provider-composer.ts:208-210 configuredApiKey returns extension?.apiKey ??
// config?.apiKey), and model-runtime.ts:672 marks the provider configured from
// that composed value. A registration that defines no apiKey therefore keeps the
// models.json key, its availability, and its models_json_key source label.
func TestModelRegistry_ExtensionRegistrationKeepsModelsJSONAuth(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	data := `{"providers":{"vllm":{"baseUrl":"http://vllm.example/v1","api":"openai-completions","apiKey":"configured-key","models":[{"id":"qwen3.8-27b","name":"qwen3.8-27b"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	r := NewModelRegistry(dir)
	if !r.HasConfiguredAuth("vllm") {
		t.Fatalf("models.json provider is not configured before the registration")
	}
	if err := r.RegisterExtensionProvider("vllm", extension.ProviderConfig{API: ai.APIOpenAICompletions}); err != nil {
		t.Fatal(err)
	}
	if !r.HasConfiguredAuth("vllm") {
		t.Errorf("HasConfiguredAuth(vllm) = false after a registration that defines no apiKey")
	}
	var found bool
	for _, e := range r.GetAvailable() {
		if e.ProviderID == "vllm" && e.ModelID == "qwen3.8-27b" {
			found = true
		}
	}
	if !found {
		t.Errorf("the models.json model left GetAvailable() after the registration")
	}
	if status := r.GetProviderAuthStatus("vllm"); !status.Configured || status.Source != ai.AuthSourceModelsJSONKey {
		t.Errorf("GetProviderAuthStatus = %+v, want configured with source %q", status, ai.AuthSourceModelsJSONKey)
	}
}

// Pi's applyModelsJson (provider-composer.ts modelFromJson) takes a custom model's base URL from the model, the provider, then the defaults
// findModelDefaults picks from the provider's built-in models, and rejects the provider configuration when none has one. The azure provider's
// built-in models ship without a base URL, so a custom azure model needs its own: Pi 1.0.3 prints `Warning: errors loading models.json:` with
// this message for `{"providers":{"azure":{"models":[{"id":"my-foundry","api":"openai-completions"}]}}}` and keeps the built-in models.
// A built-in provider whose defaults have a base URL accepts the same definition.
func TestModelsJSONCustomModelUnderABuiltInProviderNeedsABaseURL(t *testing.T) {
	has := func(registry *ModelRegistry, providerID, modelID string) bool {
		return slices.ContainsFunc(registry.GetProviderModelData(providerID), func(model *ai.Model) bool { return model.ID == modelID })
	}
	write := func(t *testing.T, content string) *ModelRegistry {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return NewModelRegistryWithModelsPath(path)
	}

	registry := write(t, `{"providers":{"azure":{"models":[{"id":"my-foundry","api":"openai-completions"}]}}}`)
	if want := `Provider "azure": Provider azure: "baseUrl" is required when defining custom models.`; !strings.Contains(registry.LoadError(), want) {
		t.Fatalf("load error = %q, want %q", registry.LoadError(), want)
	}
	if has(registry, "azure", "my-foundry") {
		t.Fatal("the custom azure model without a base URL was added")
	}
	if !has(registry, "azure", "gpt-5.4") {
		t.Fatal("the built-in azure models were dropped with the configuration")
	}

	registry = write(t, `{"providers":{"azure":{"baseUrl":"https://r.openai.azure.com/openai/v1","models":[{"id":"my-foundry","api":"openai-completions"}]}}}`)
	if registry.LoadError() != "" {
		t.Fatalf("load error with a provider base URL = %q", registry.LoadError())
	}
	if !has(registry, "azure", "my-foundry") {
		t.Fatal("the custom azure model with a provider base URL is missing")
	}

	registry = write(t, `{"providers":{"openai":{"models":[{"id":"x","api":"openai-completions"}]}}}`)
	if registry.LoadError() != "" {
		t.Fatalf("load error for a provider whose defaults have a base URL = %q", registry.LoadError())
	}
	if !has(registry, "openai", "x") {
		t.Fatal("the custom openai model inherits the built-in base URL")
	}
}

// provider-composer.ts:224-229 modelFromJson: a models.json model whose contextWindow or maxTokens is zero or negative throws
// "Provider <id>, model <id>: invalid contextWindow|maxTokens", the runtime records the composition error and keeps the base models
// (here none: the provider is dropped), and the checks run per model in definition order after the base URL check.
func TestModelsJSONModelWithANonPositiveContextWindowOrMaxTokensIsRejected(t *testing.T) {
	has := func(registry *ModelRegistry, modelID string) bool {
		return slices.ContainsFunc(registry.GetProviderModelData("custom"), func(model *ai.Model) bool { return model.ID == modelID })
	}
	load := func(t *testing.T, models string) *ModelRegistry {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.json")
		content := `{"providers":{"custom":{"baseUrl":"https://custom.test/v1","api":"openai-completions","models":` + models + `}}}`
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return NewModelRegistryWithModelsPath(path)
	}
	for _, c := range []struct{ name, models, want string }{
		{"context window zero", `[{"id":"ok"},{"id":"bad","contextWindow":0}]`, `Provider "custom": Provider custom, model bad: invalid contextWindow`},
		{"context window negative", `[{"id":"bad","contextWindow":-5}]`, `Provider custom, model bad: invalid contextWindow`},
		{"max tokens zero", `[{"id":"bad","maxTokens":0}]`, `Provider custom, model bad: invalid maxTokens`},
		{"context window first", `[{"id":"bad","contextWindow":-1,"maxTokens":-1}]`, `Provider custom, model bad: invalid contextWindow`},
	} {
		t.Run(c.name, func(t *testing.T) {
			registry := load(t, c.models)
			if !strings.Contains(registry.LoadError(), c.want) {
				t.Fatalf("load error = %q, want %q", registry.LoadError(), c.want)
			}
			if has(registry, "bad") || has(registry, "ok") {
				t.Fatal("a provider with an invalid model definition was composed")
			}
		})
	}
	registry := load(t, `[{"id":"fine","contextWindow":1,"maxTokens":1}]`)
	if registry.LoadError() != "" || !has(registry, "fine") {
		t.Fatalf("positive limits: error %q, has fine %v", registry.LoadError(), has(registry, "fine"))
	}
}

// provider-composer.ts:216-221 modelFromJson: a models.json model with no api (its own, the provider's, or the one of the built-in model
// findModelDefaults picks) throws `Provider <id>, model <id>: no "api" specified. Set at provider or model level.`, which is checked before
// the base URL. An "oauth" provider's base models are not known when the file is read, so it is not judged here.
func TestModelsJSONModelWithoutAnAPIIsRejected(t *testing.T) {
	load := func(t *testing.T, providers string) *ModelRegistry {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, []byte(`{"providers":`+providers+`}`), 0o600); err != nil {
			t.Fatal(err)
		}
		return NewModelRegistryWithModelsPath(path)
	}
	has := func(registry *ModelRegistry, providerID, modelID string) bool {
		return slices.ContainsFunc(registry.GetProviderModelData(providerID), func(model *ai.Model) bool { return model.ID == modelID })
	}
	registry := load(t, `{"custom":{"apiKey":"k","models":[{"id":"m"}]}}`)
	if want := `Provider "custom": Provider custom, model m: no "api" specified. Set at provider or model level.`; !strings.Contains(registry.LoadError(), want) {
		t.Fatalf("load error = %q, want %q (the api check comes before the base URL check)", registry.LoadError(), want)
	}
	if has(registry, "custom", "m") {
		t.Fatal("a model without an api was composed")
	}
	for name, providers := range map[string]string{
		"provider api":   `{"custom":{"baseUrl":"https://c.test/v1","api":"openai-completions","apiKey":"k","models":[{"id":"m"}]}}`,
		"model api":      `{"custom":{"baseUrl":"https://c.test/v1","apiKey":"k","models":[{"id":"m","api":"openai-completions"}]}}`,
		"built-in model": `{"openai":{"models":[{"id":"my-gpt"}]}}`,
	} {
		registry := load(t, providers)
		providerID := "custom"
		if name == "built-in model" {
			providerID = "openai"
		}
		if registry.LoadError() != "" || !has(registry, providerID, map[bool]string{true: "my-gpt", false: "m"}[name == "built-in model"]) {
			t.Errorf("%s: load error %q", name, registry.LoadError())
		}
	}
}
