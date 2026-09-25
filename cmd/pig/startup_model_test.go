package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Upstream model-resolver.test.ts mock models.
var (
	resolverMockModels = []codingagent.RuntimeModel{
		{Provider: "anthropic", ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5"},
		{Provider: "openai", ID: "gpt-4o", Name: "GPT-4o"},
	}
	resolverMockOpenRouterModels = []codingagent.RuntimeModel{
		{Provider: "openrouter", ID: "qwen/qwen3-coder:exacto", Name: "Qwen3 Coder Exacto"},
		{Provider: "openrouter", ID: "openai/gpt-4o:extended", Name: "GPT-4o Extended"},
	}
	resolverAllModels = append(slices.Clone(resolverMockModels), resolverMockOpenRouterModels...)
)

func mockStartupRuntime(models []codingagent.RuntimeModel, authenticated ...string) *startupModelRuntime {
	return newStartupModelRuntime(models, func(provider string) bool { return slices.Contains(authenticated, provider) })
}

func scopedRefs(scoped []ScopedModel) []string {
	refs := make([]string, 0, len(scoped))
	for _, entry := range scoped {
		refs = append(refs, modelRef(entry.Model)+":"+entry.ThinkingLevel)
	}
	return refs
}

// Ports upstream model-resolver.test.ts resolveModelScopeWithDiagnostics.
func TestResolveModelScopeFromModelsMatchesUpstream(t *testing.T) {
	bracketed := codingagent.RuntimeModel{Provider: "custom", ID: "bracketed-model[1m]", Name: "Bracketed Model"}
	for _, tc := range []struct {
		name     string
		patterns []string
		models   []codingagent.RuntimeModel
		want     []string
		warnings []string
	}{
		{
			name:     "returns scoped models and structured diagnostics",
			patterns: []string{"sonnet:high", "gpt-4o:invalid", "missing"},
			models:   resolverAllModels,
			want:     []string{"anthropic/claude-sonnet-4-5:high", "openai/gpt-4o:"},
			warnings: []string{
				`Invalid thinking level "invalid" in pattern "gpt-4o:invalid". Using default instead.`,
				`No models match pattern "missing"`,
			},
		},
		{
			name:     "resolves bracketed model ids as exact references before glob matching",
			patterns: []string{"custom/bracketed-model[1m]"},
			models:   append(slices.Clone(resolverAllModels), bracketed),
			want:     []string{"custom/bracketed-model[1m]:"},
		},
		{
			name:     "resolves bracketed model ids with thinking levels as exact references",
			patterns: []string{"custom/bracketed-model[1m]:high"},
			models:   append(slices.Clone(resolverAllModels), bracketed),
			want:     []string{"custom/bracketed-model[1m]:high"},
		},
		{
			name:     "glob matches provider/id or bare id and applies the thinking suffix",
			patterns: []string{"*gpt-4o*:low", "anthropic/*"},
			models:   resolverAllModels,
			want:     []string{"openai/gpt-4o:low", "anthropic/claude-sonnet-4-5:"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scoped, warnings := resolveModelScopeFromModels(tc.patterns, tc.models)
			if got := scopedRefs(scoped); !slices.Equal(got, tc.want) {
				t.Fatalf("scoped = %q, want %q", got, tc.want)
			}
			if !slices.Equal(warnings, tc.warnings) {
				t.Fatalf("warnings = %q, want %q", warnings, tc.warnings)
			}
		})
	}
}

// Ports upstream model-resolver.test.ts resolveCliModel cases over the
// runtime startup model selection reads.
func TestResolveCliModelMatchesUpstream(t *testing.T) {
	azure := codingagent.RuntimeModel{Provider: "azure-openai-responses", ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol"}
	codex := codingagent.RuntimeModel{Provider: "openai-codex", ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol"}
	zai := codingagent.RuntimeModel{Provider: "zai", ID: "glm-5", Name: "GLM-5"}
	gateway := codingagent.RuntimeModel{Provider: "vercel-ai-gateway", ID: "zai/glm-5", Name: "GLM-5"}
	for _, tc := range []struct {
		name          string
		provider      string
		model         string
		models        []codingagent.RuntimeModel
		authenticated []string
		want          string
		thinking      string
		errContains   string
	}{
		{name: "resolves --model provider/id without --provider", model: "openai/gpt-4o", models: resolverAllModels, want: "openai/gpt-4o"},
		{name: "resolves fuzzy patterns within an explicit provider", provider: "openai", model: "4o", models: resolverAllModels, want: "openai/gpt-4o"},
		{name: "supports --model <pattern>:<thinking>", model: "sonnet:high", models: resolverAllModels, want: "anthropic/claude-sonnet-4-5", thinking: "high"},
		{name: "prefers exact model id match over provider inference", model: "openai/gpt-4o:extended", models: resolverAllModels, want: "openrouter/openai/gpt-4o:extended"},
		{name: "does not strip an invalid suffix as a thinking level", provider: "openai", model: "gpt-4o:extended", models: resolverAllModels, want: "openai/gpt-4o:extended"},
		{name: "allows custom ids for explicit providers without double prefixing", provider: "openrouter", model: "openrouter/openai/ghost-model", models: resolverAllModels, want: "openrouter/openai/ghost-model"},
		{name: "returns a clear error when there are no models", provider: "openai", model: "gpt-4o", errContains: "No models available"},
		{name: "prefers the sole authenticated provider for an ambiguous bare id", model: "gpt-5.6-sol", models: []codingagent.RuntimeModel{azure, codex}, authenticated: []string{"openai-codex"}, want: "openai-codex/gpt-5.6-sol"},
		{name: "requires a provider for an ambiguous bare id", model: "gpt-5.6-sol", models: []codingagent.RuntimeModel{azure, codex}, errContains: "Use --provider or provider/model"},
		{name: "prefers provider/model split over a gateway id", model: "zai/glm-5", models: append(slices.Clone(resolverAllModels), zai, gateway), authenticated: []string{"zai", "vercel-ai-gateway"}, want: "zai/glm-5"},
		{name: "resolves provider-prefixed fuzzy patterns", model: "openrouter/qwen", models: resolverAllModels, want: "openrouter/qwen/qwen3-coder:exacto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := ResolveCliModel(tc.provider, tc.model, "", mockStartupRuntime(tc.models, tc.authenticated...))
			if tc.errContains != "" {
				if result.Model != nil || !strings.Contains(result.Error, tc.errContains) {
					t.Fatalf("result = %+v, want error containing %q", result, tc.errContains)
				}
				return
			}
			if result.Error != "" || result.Model == nil {
				t.Fatalf("result = %+v, want %s", result, tc.want)
			}
			if got := modelRef(*result.Model); got != tc.want || result.ThinkingLevel != tc.thinking {
				t.Fatalf("model = %s:%s, want %s:%s", got, result.ThinkingLevel, tc.want, tc.thinking)
			}
		})
	}
}

// Ports upstream model-resolver.test.ts findInitialModel cases.
func TestFindInitialModelMatchesUpstream(t *testing.T) {
	gatewayOpus := codingagent.RuntimeModel{Provider: "vercel-ai-gateway", ID: "anthropic/claude-opus-4-6"}
	savedDeepSeek := codingagent.RuntimeModel{Provider: "deepseek", ID: "deepseek-v4-flash"}
	localDeepSeek := codingagent.RuntimeModel{Provider: "spark-two", ID: "deepseek-v4-flash"}
	anthropicDefault := codingagent.RuntimeModel{Provider: "anthropic", ID: "claude-opus-4-8"}
	openAIDefault := codingagent.RuntimeModel{Provider: "openai", ID: "gpt-5.5"}
	for _, tc := range []struct {
		name            string
		models          []codingagent.RuntimeModel
		authenticated   []string
		defaultProvider string
		defaultModel    string
		want            string
	}{
		{name: "selects the ai-gateway default when available", models: []codingagent.RuntimeModel{gatewayOpus}, authenticated: []string{"vercel-ai-gateway"}, want: "vercel-ai-gateway/anthropic/claude-opus-4-6"},
		{name: "ignores an unauthenticated saved default", models: []codingagent.RuntimeModel{savedDeepSeek, localDeepSeek}, authenticated: []string{"spark-two"}, defaultProvider: "deepseek", defaultModel: "deepseek-v4-flash", want: "spark-two/deepseek-v4-flash"},
		{name: "uses an authenticated saved default", models: []codingagent.RuntimeModel{openAIDefault, savedDeepSeek}, authenticated: []string{"deepseek", "openai"}, defaultProvider: "deepseek", defaultModel: "deepseek-v4-flash", want: "deepseek/deepseek-v4-flash"},
		{name: "walks known provider defaults in declaration order", models: []codingagent.RuntimeModel{localDeepSeek, openAIDefault, anthropicDefault}, authenticated: []string{"spark-two", "openai", "anthropic"}, want: "anthropic/claude-opus-4-8"},
		{name: "falls back to the first available model", models: []codingagent.RuntimeModel{savedDeepSeek, localDeepSeek}, authenticated: []string{"spark-two"}, want: "spark-two/deepseek-v4-flash"},
		{name: "no available model", models: []codingagent.RuntimeModel{savedDeepSeek}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := findInitialModel(mockStartupRuntime(tc.models, tc.authenticated...), tc.defaultProvider, tc.defaultModel)
			ref := ""
			if got != nil {
				ref = modelRef(*got)
			}
			if ref != tc.want {
				t.Fatalf("findInitialModel = %q, want %q", ref, tc.want)
			}
		})
	}
}

// isolateProviderAuthEnv clears ambient provider credentials so startup model
// selection sees only what a test sets.
func isolateProviderAuthEnv(t *testing.T) string {
	t.Helper()
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_TOKEN") || strings.HasPrefix(name, "AWS_") ||
			strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "GOOGLE_") || strings.HasPrefix(name, "AZURE_") {
			t.Setenv(name, "")
		}
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	previous := agentDirForModelOverride
	agentDirForModelOverride = dir
	t.Cleanup(func() { agentDirForModelOverride = previous })
	return dir
}

// TestSelectStartupModelMatchesUpstream drives startup model selection over
// the real catalog. MAIN-01: a built-in provider's env key alone picks its
// default model. MAIN-02: a bare --model resolves its provider from the
// registry. MAIN-03: a saved default without auth falls back. MAIN-04: the
// --models scope picks a new session's starting model and thinking level.
func TestSelectStartupModelMatchesUpstream(t *testing.T) {
	for _, tc := range []struct {
		name         string
		options      startupModelOptions
		settings     codingagent.Settings
		want         string
		thinking     string
		errContains  string
		warnContains string
	}{
		{name: "built-in env key picks the provider default", want: "anthropic/claude-opus-4-8"},
		{name: "bare --model resolves the provider from the registry", options: startupModelOptions{CLIModel: "claude-sonnet-4-5"}, settings: codingagent.Settings{DefaultProvider: "openai"}, want: "anthropic/claude-sonnet-4-5"},
		{name: "bare --model with a thinking suffix", options: startupModelOptions{CLIModel: "anthropic/claude-sonnet-4-5:high"}, want: "anthropic/claude-sonnet-4-5", thinking: "high"},
		{name: "unknown bare --model is an error", options: startupModelOptions{CLIModel: "no-such-model-xyz"}, errContains: `Model "no-such-model-xyz" not found`},
		{name: "unknown --provider is an error", options: startupModelOptions{CLIProvider: "no-such-provider", CLIModel: "x"}, errContains: `Unknown provider "no-such-provider"`},
		{name: "--provider without --model is ignored", options: startupModelOptions{CLIProvider: "openai"}, want: "anthropic/claude-opus-4-8"},
		{name: "saved default without auth falls back", settings: codingagent.Settings{DefaultProvider: "openai", DefaultModel: "gpt-4o-mini"}, want: "anthropic/claude-opus-4-8"},
		{name: "saved default with auth is used", settings: codingagent.Settings{DefaultProvider: "anthropic", DefaultModel: "claude-sonnet-4-5"}, want: "anthropic/claude-sonnet-4-5"},
		{name: "scope picks a new session's model and thinking", options: startupModelOptions{ScopePatterns: []string{"anthropic/claude-sonnet-4-5:low", "anthropic/claude-haiku-4-5"}}, want: "anthropic/claude-sonnet-4-5", thinking: "low"},
		{name: "scope prefers the saved default in scope", options: startupModelOptions{ScopePatterns: []string{"anthropic/claude-sonnet-4-5", "anthropic/claude-haiku-4-5:high"}}, settings: codingagent.Settings{DefaultProvider: "anthropic", DefaultModel: "claude-haiku-4-5"}, want: "anthropic/claude-haiku-4-5", thinking: "high"},
		{name: "scope is skipped for a continuing session", options: startupModelOptions{ScopePatterns: []string{"anthropic/claude-sonnet-4-5"}, Continuing: true}, want: "anthropic/claude-opus-4-8"},
		{name: "scope pattern without a match warns", options: startupModelOptions{ScopePatterns: []string{"missing-model-xyz"}}, want: "anthropic/claude-opus-4-8", warnContains: `No models match pattern "missing-model-xyz"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := isolateProviderAuthEnv(t)
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
			registry := codingagent.NewModelRegistry(dir)
			selected, err := selectStartupModel(context.Background(), tc.options, tc.settings, registry)
			if tc.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("err = %v, want %q", err, tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if selected.Model == nil {
				t.Fatalf("no model selected, want %s", tc.want)
			}
			if got := selected.Model.ProviderMeta.ProviderID + "/" + selected.Model.ID; got != tc.want || selected.Thinking != tc.thinking {
				t.Fatalf("model = %s:%s, want %s:%s", got, selected.Thinking, tc.want, tc.thinking)
			}
			if tc.warnContains != "" && !slices.ContainsFunc(selected.Warnings, func(w string) bool { return strings.Contains(w, tc.warnContains) }) {
				t.Fatalf("warnings = %q, want %q", selected.Warnings, tc.warnContains)
			}
		})
	}
}

// TestSelectStartupModelUsesStoredLoginProvider covers a /login credential
// as the only auth: its provider is available and supplies the default.
func TestSelectStartupModelUsesStoredLoginProvider(t *testing.T) {
	dir := isolateProviderAuthEnv(t)
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"openai":{"type":"api_key","key":"stored-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := codingagent.NewModelRegistry(dir)
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry.SetAuthStorage(auth)
	selected, err := selectStartupModel(context.Background(), startupModelOptions{}, codingagent.Settings{}, registry)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Model == nil || selected.Model.ProviderMeta.ProviderID != "openai" || selected.Model.ID != "gpt-5.5" {
		t.Fatalf("model = %+v, want openai/gpt-5.5", selected.Model)
	}
}

// TestSelectStartupModelAPIKey mirrors upstream main.ts --api-key handling:
// the key becomes a non-persistent runtime credential for the CLI model's
// provider that outranks stored and env keys, and it requires a model from
// --model or --models.
func TestSelectStartupModelAPIKey(t *testing.T) {
	t.Run("sets a runtime key for the CLI model provider", func(t *testing.T) {
		authorization := make(chan string, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case authorization <- r.Header.Get("Authorization"):
			default:
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}))
		defer server.Close()
		dir := isolateProviderAuthEnv(t)
		t.Setenv("OPENAI_API_KEY", "env-key")
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"openai":{"type":"api_key","key":"stored-key"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(`{"providers":{"openai":{"baseUrl":"`+server.URL+`"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		registry := codingagent.NewModelRegistry(dir)
		selected, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: "openai/gpt-4o-mini", APIKey: "cli-key"}, codingagent.Settings{}, registry)
		if err != nil {
			t.Fatal(err)
		}
		if key, ok := registry.RuntimeAPIKey("openai"); !ok || key != "cli-key" {
			t.Fatalf("runtime key = %q, %v", key, ok)
		}
		transcript := ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("Hello")}}})
		stream, err := selected.Model.Provider.Stream(context.Background(), transcript, ai.StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for range stream.Events(context.Background()) {
		}
		if got := <-authorization; got != "Bearer cli-key" {
			t.Fatalf("Authorization = %q, want the --api-key", got)
		}
		stored, err := os.ReadFile(filepath.Join(dir, "auth.json"))
		if err != nil || strings.Contains(string(stored), "cli-key") {
			t.Fatalf("auth.json = %s, %v; the runtime key must not persist", stored, err)
		}
	})
	t.Run("requires a CLI or scoped model", func(t *testing.T) {
		dir := isolateProviderAuthEnv(t)
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
		_, err := selectStartupModel(context.Background(), startupModelOptions{APIKey: "cli-key"}, codingagent.Settings{}, codingagent.NewModelRegistry(dir))
		if err == nil || err.Error() != "--api-key requires a model to be specified via --model, --provider/--model, or --models" {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("a scoped model carries the key", func(t *testing.T) {
		dir := isolateProviderAuthEnv(t)
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
		registry := codingagent.NewModelRegistry(dir)
		if _, err := selectStartupModel(context.Background(), startupModelOptions{ScopePatterns: []string{"anthropic/claude-sonnet-4-5"}, APIKey: "cli-key"}, codingagent.Settings{}, registry); err != nil {
			t.Fatal(err)
		}
		if key, ok := registry.RuntimeAPIKey("anthropic"); !ok || key != "cli-key" {
			t.Fatalf("runtime key = %q, %v", key, ok)
		}
	})
}

// TestSelectStartupModelKeepsTestFaux keeps the uncatalogued test-only
// provider selectable by --model when PIG_TEST_FAUX=1.
func TestSelectStartupModelKeepsTestFaux(t *testing.T) {
	dir := isolateProviderAuthEnv(t)
	t.Setenv("PIG_TEST_FAUX", "1")
	for _, options := range []startupModelOptions{{CLIModel: "test-faux/faux-1:high"}, {CLIProvider: "test-faux", CLIModel: "faux-1:high"}} {
		selected, err := selectStartupModel(context.Background(), options, codingagent.Settings{}, codingagent.NewModelRegistry(dir))
		if err != nil {
			t.Fatal(err)
		}
		if selected.Model == nil || selected.Model.ProviderMeta.ProviderID != "test-faux" || selected.Model.ID != "faux-1" || selected.Thinking != "high" {
			t.Fatalf("%+v: model = %+v thinking %q", options, selected.Model, selected.Thinking)
		}
	}
}

// TestSelectStartupModelHidesTestFauxWithoutOptIn keeps the test-only
// provider out of normal runs (CH-021): without PIG_TEST_FAUX=1, --model
// test-faux/... resolves like any unknown model.
func TestSelectStartupModelHidesTestFauxWithoutOptIn(t *testing.T) {
	dir := isolateProviderAuthEnv(t)
	t.Setenv("PIG_TEST_FAUX", "")
	_, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: "test-faux/faux-1"}, codingagent.Settings{}, codingagent.NewModelRegistry(dir))
	if err == nil || !strings.Contains(err.Error(), `Model "test-faux/faux-1" not found`) {
		t.Fatalf("err = %v, want upstream's not-found error", err)
	}
}
