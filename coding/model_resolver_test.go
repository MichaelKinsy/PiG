package coding

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/model-resolver.test.ts (v1.1.0) through the public entry points over a real ModelRuntime: the "resolveModelScopeWithDiagnostics" cases (:224) and the "resolveCliModel" cases (:321).

func resolverRuntimeFixture(t *testing.T, providers ...string) *ModelRuntime {
	t.Helper()
	services := newTestServices(t)
	for _, provider := range providers {
		services.Registry().SetRuntimeAPIKey(provider, "test-key")
	}
	return services.ModelRuntime()
}

func scopedModelIDs(result ResolveModelScopeResult) []string {
	ids := make([]string, 0, len(result.ScopedModels))
	for _, scoped := range result.ScopedModels {
		ids = append(ids, scoped.Model.ID)
	}
	return ids
}

// model-resolver.test.ts:224 "returns scoped models and structured diagnostics without writing console warnings".
func TestResolveModelScopeWithDiagnosticsOverModelRuntime(t *testing.T) {
	runtime := resolverRuntimeFixture(t, "anthropic", "openai")
	result, err := ResolveModelScopeWithDiagnostics(context.Background(), []string{"sonnet:high", "gpt-4o:invalid", "missing"}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if got := scopedModelIDs(result); len(got) != 2 || got[1] != "gpt-4o" || got[0] == "" {
		t.Fatalf("scoped ids = %v, want [<sonnet> gpt-4o]", got)
	}
	if got := result.ScopedModels[0]; got.ThinkingLevel != "high" || got.Model.ProviderMeta.ProviderID != "anthropic" {
		t.Fatalf("first = %s/%s %q, want an anthropic sonnet at high", got.Model.ProviderMeta.ProviderID, got.Model.ID, got.ThinkingLevel)
	}
	if got := result.ScopedModels[1].ThinkingLevel; got != "" {
		t.Fatalf("second thinking level = %q, want unset", got)
	}
	want := []ModelScopeDiagnostic{
		{Type: "warning", Message: `Invalid thinking level "invalid" in pattern "gpt-4o:invalid". Using default instead.`, Code: "invalid-thinking-level", Pattern: "gpt-4o:invalid"},
		{Type: "warning", Message: `No models match pattern "missing"`, Code: "no-match", Pattern: "missing"},
	}
	if !slices.Equal(result.Diagnostics, want) {
		t.Fatalf("diagnostics = %+v, want %+v", result.Diagnostics, want)
	}
	// The scoped model is the runtime's own bound model, with every request field.
	if native := runtime.GetModel("openai", "gpt-4o"); native == nil || result.ScopedModels[1].Model.Capabilities.ContextWindow != native.Capabilities.ContextWindow || result.ScopedModels[1].Model.ProviderMeta.BaseURL != native.ProviderMeta.BaseURL {
		t.Fatalf("scoped model %+v is not the runtime model %+v", result.ScopedModels[1].Model, native)
	}
}

// resolveModelScope reads getAvailable: a model of a provider without configured auth is not in scope, while resolveCliModel reads every catalog model.
func TestResolveModelScopeReadsAvailableModelsOnly(t *testing.T) {
	runtime := resolverRuntimeFixture(t, "anthropic")
	result, err := ResolveModelScopeWithDiagnostics(context.Background(), []string{"gpt-4o"}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ScopedModels) != 0 || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "no-match" {
		t.Fatalf("scope = %v, diagnostics = %+v, want an empty scope and one no-match", scopedModelIDs(result), result.Diagnostics)
	}
	cli := ResolveCliModel(ResolveCliModelOptions{CliModel: "openai/gpt-4o", ModelRuntime: runtime})
	if cli.Error != "" || cli.Model == nil || cli.Model.ProviderMeta.ProviderID != "openai" || cli.Model.ID != "gpt-4o" {
		t.Fatalf("resolveCliModel = %+v, want openai/gpt-4o from the whole catalog", cli)
	}
}

func openRouterPrefixedID(t *testing.T, runtime *ModelRuntime) string {
	t.Helper()
	for _, model := range runtime.GetModels("openrouter") {
		// A prefix naming a known provider whose catalog lacks the rest, so inference finds no model and the exact id is the only match.
		provider, rest, ok := strings.Cut(model.ID, "/")
		if ok && runtime.GetProvider(provider) != nil && runtime.GetModel(provider, rest) == nil {
			return model.ID
		}
	}
	t.Fatal("the catalog has no OpenRouter id whose prefix names another provider that lacks the rest")
	return ""
}

// model-resolver.test.ts:321 "resolves --model provider/id without --provider", and the thinking suffix cases.
func TestResolveCliModelOverModelRuntime(t *testing.T) {
	runtime := resolverRuntimeFixture(t)
	for _, tc := range []struct {
		name     string
		options  ResolveCliModelOptions
		provider string
		id       string
		level    ai.ThinkingLevel
		warning  bool
	}{
		{name: "provider/id", options: ResolveCliModelOptions{CliModel: "openai/gpt-4o"}, provider: "openai", id: "gpt-4o"},
		{name: "--provider and --model", options: ResolveCliModelOptions{CliProvider: "openai", CliModel: "gpt-4o"}, provider: "openai", id: "gpt-4o"},
		// :330 "resolves fuzzy patterns within an explicit provider"
		{name: "fuzzy within a provider", options: ResolveCliModelOptions{CliProvider: "openai", CliModel: "4o"}, provider: "openai", id: "gpt-4o-mini"},
		// :353 "supports --model <pattern>:<thinking> (without explicit --thinking)"
		{name: "thinking suffix", options: ResolveCliModelOptions{CliProvider: "anthropic", CliModel: "claude-sonnet-4-5:high"}, provider: "anthropic", id: "claude-sonnet-4-5", level: "high"},
		// :370 "prefers exact model id match over provider inference (OpenRouter-style ids)": the bundled catalog's OpenRouter models are the provider-prefixed ids.
		{name: "exact id beats provider inference", options: ResolveCliModelOptions{CliModel: openRouterPrefixedID(t, runtime)}, provider: "openrouter", id: openRouterPrefixedID(t, runtime)},
		// :386 "does not strip invalid :suffix as thinking level in --model (treat as raw id)"
		{name: "invalid suffix is a raw id", options: ResolveCliModelOptions{CliProvider: "openai", CliModel: "gpt-4o:extended"}, provider: "openai", id: "gpt-4o:extended", warning: true},
		// :402 "allows custom model ids for explicit providers without double prefixing"
		{name: "no double prefix", options: ResolveCliModelOptions{CliProvider: "openrouter", CliModel: "openrouter/openai/ghost-model"}, provider: "openrouter", id: "openai/ghost-model", warning: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.options.ModelRuntime = runtime
			result := ResolveCliModel(tc.options)
			if result.Error != "" || result.Model == nil {
				t.Fatalf("result = %+v", result)
			}
			if result.Model.ProviderMeta.ProviderID != tc.provider || result.Model.ID != tc.id || result.ThinkingLevel != tc.level || (result.Warning != "") != tc.warning {
				t.Fatalf("result = %s/%s level %q warning %q, want %s/%s level %q warning=%v", result.Model.ProviderMeta.ProviderID, result.Model.ID, result.ThinkingLevel, result.Warning, tc.provider, tc.id, tc.level, tc.warning)
			}
		})
	}
	t.Run("a custom id under a known provider builds a fallback model", func(t *testing.T) {
		result := ResolveCliModel(ResolveCliModelOptions{CliProvider: "anthropic", CliModel: "my-custom-model", ModelRuntime: runtime})
		if result.Error != "" || result.Model == nil || result.Model.ID != "my-custom-model" || result.Model.DisplayName != "my-custom-model" || result.Model.ProviderMeta.ProviderID != "anthropic" || result.Warning == "" {
			t.Fatalf("result = %+v", result)
		}
		if runtime.GetModel("anthropic", "my-custom-model") != nil {
			t.Fatal("the fallback model was registered in the runtime catalog")
		}
	})
	t.Run("no model selects nothing", func(t *testing.T) {
		if result := ResolveCliModel(ResolveCliModelOptions{ModelRuntime: runtime}); result != (ResolveCliModelResult{}) {
			t.Fatalf("result = %+v, want zero", result)
		}
	})
}
