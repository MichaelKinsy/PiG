package codingagent

import (
	"reflect"
	"testing"
)

type initialModelTestRuntime struct {
	models     []RuntimeModel
	available  []RuntimeModel
	configured map[string]bool
	calls      []string
}

func (runtime *initialModelTestRuntime) GetModels(provider string) []RuntimeModel {
	runtime.calls = append(runtime.calls, "models:"+provider)
	return runtime.models
}
func (runtime *initialModelTestRuntime) HasConfiguredAuth(provider string) bool {
	runtime.calls = append(runtime.calls, "auth:"+provider)
	return runtime.configured[provider]
}
func (runtime *initialModelTestRuntime) GetModel(provider, id string) *RuntimeModel {
	runtime.calls = append(runtime.calls, "model:"+provider+"/"+id)
	for index := range runtime.models {
		model := &runtime.models[index]
		if model.Provider == provider && model.ID == id {
			return model
		}
	}
	return nil
}
func (runtime *initialModelTestRuntime) GetAvailableSnapshot() []RuntimeModel {
	runtime.calls = append(runtime.calls, "available")
	return runtime.available
}

// upstream: packages/coding-agent/src/core/model-resolver.ts:683-720 and model-runtime.ts:423-424,467-468. Availability is an independent snapshot, not the catalog filtered by configured providers.
func TestFindInitialModelUsesAvailableSnapshotAndSavedIdentity(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, savedProvider, savedID string
		models, available            []RuntimeModel
		configured                   map[string]bool
		want                         string
		thinking                     string
		calls                        []string
		wantSavedIdentity            bool
	}{
		{
			name:       "configured catalog default is unavailable",
			models:     []RuntimeModel{{Provider: "openai", ID: "gpt-5.5"}},
			available:  []RuntimeModel{{Provider: "custom-endpoint", ID: "second"}, {Provider: "custom-endpoint", ID: "first"}},
			configured: map[string]bool{"openai": true}, want: "custom-endpoint/second", thinking: DefaultThinkingLevel,
			calls: []string{"available"},
		},
		{
			name:   "empty snapshot does not reconstruct configured catalog",
			models: []RuntimeModel{{Provider: "openai", ID: "gpt-5.5"}}, configured: map[string]bool{"openai": true},
			thinking: DefaultThinkingLevel, calls: []string{"available"},
		},
		{
			name: "authenticated saved model need not be available", savedProvider: "oauth-provider", savedID: "saved",
			models: []RuntimeModel{{Provider: "oauth-provider", ID: "saved"}}, configured: map[string]bool{"oauth-provider": true},
			want: "oauth-provider/saved", thinking: "low", calls: []string{"model:oauth-provider/saved", "auth:oauth-provider"}, wantSavedIdentity: true,
		},
		{
			name: "unauthenticated saved model falls back", savedProvider: "oauth-provider", savedID: "saved",
			models: []RuntimeModel{{Provider: "oauth-provider", ID: "saved"}}, available: []RuntimeModel{{Provider: "api-key-provider", ID: "available"}},
			want: "api-key-provider/available", thinking: DefaultThinkingLevel, calls: []string{"model:oauth-provider/saved", "auth:oauth-provider", "available"},
		},
		{
			name: "missing saved model does not check auth", savedProvider: "absent", savedID: "saved",
			available: []RuntimeModel{{Provider: "no-default-provider", ID: "first"}}, want: "no-default-provider/first", thinking: DefaultThinkingLevel,
			calls: []string{"model:absent/saved", "available"},
		},
		{
			name:      "provider declaration order precedes snapshot order",
			available: []RuntimeModel{{Provider: "openai", ID: "gpt-5.5"}, {Provider: "anthropic", ID: "claude-opus-4-8"}},
			want:      "anthropic/claude-opus-4-8", thinking: DefaultThinkingLevel, calls: []string{"available"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			runtime := &initialModelTestRuntime{models: row.models, available: row.available, configured: row.configured}
			got, err := FindInitialModel(FindInitialModelOptions{ModelRuntime: runtime, IsContinuing: true, DefaultProvider: row.savedProvider, DefaultModelId: row.savedID, DefaultThinkingLevel: "low"})
			if err != nil {
				t.Fatal(err)
			}
			ref := ""
			if got.Model != nil {
				ref = modelRef(*got.Model)
			}
			if ref != row.want || got.ThinkingLevel != row.thinking || got.FallbackMessage != "" || !reflect.DeepEqual(runtime.calls, row.calls) {
				t.Fatalf("result=%+v model=%q calls=%v, want model=%q thinking=%q calls=%v", got, ref, runtime.calls, row.want, row.thinking, row.calls)
			}
			if row.wantSavedIdentity && got.Model != &runtime.models[0] {
				t.Fatal("saved selection replaced the exact runtime model")
			}
		})
	}
}

// upstream: packages/coding-agent/src/core/model-resolver.ts:669-697. Scoped and saved selections have different precedence from available-model fallbacks.
func TestFindInitialModelThinkingPrecedence(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, scoped, perModel, saved, want string
		useScope, continuing                bool
	}{
		{name: "scope explicit", useScope: true, scoped: "high", perModel: "low", saved: "minimal", want: "high"},
		{name: "scope per model", useScope: true, perModel: "low", saved: "minimal", want: "low"},
		{name: "scope saved default", useScope: true, saved: "minimal", want: "minimal"},
		{name: "scope default", useScope: true, want: DefaultThinkingLevel},
		{name: "scope explicit off", useScope: true, scoped: "off", perModel: "high", saved: "low", want: "off"},
		{name: "continuing ignores scope", useScope: true, continuing: true, scoped: "high", perModel: "low", saved: "minimal", want: "low"},
		{name: "saved per model", perModel: "high", saved: "low", want: "high"},
		{name: "saved per model off", perModel: "off", saved: "high", want: "off"},
		{name: "saved default", saved: "low", want: "low"},
		{name: "saved builtin default", want: DefaultThinkingLevel},
	} {
		t.Run(row.name, func(t *testing.T) {
			model := RuntimeModel{Provider: "custom", ID: "chosen"}
			runtime := &initialModelTestRuntime{models: []RuntimeModel{model}, configured: map[string]bool{"custom": true}}
			options := FindInitialModelOptions{ModelRuntime: runtime, IsContinuing: row.continuing, DefaultProvider: "custom", DefaultModelId: "chosen", DefaultThinkingLevel: row.saved, ModelThinkingLevels: map[string]string{"custom/chosen": row.perModel}}
			if row.useScope {
				options.ScopedModels = []ScopedModel{{Model: model, ThinkingLevel: row.scoped}}
			}
			got, err := FindInitialModel(options)
			if err != nil || got.Model == nil || !reflect.DeepEqual(*got.Model, model) || got.ThinkingLevel != row.want || got.FallbackMessage != "" {
				t.Fatalf("result=%+v error=%v, want model=%+v thinking=%q", got, err, model, row.want)
			}
			if row.useScope && !row.continuing {
				if len(runtime.calls) != 0 || got.Model != &options.ScopedModels[0].Model {
					t.Fatalf("scope selection read runtime or replaced model: calls=%v", runtime.calls)
				}
			} else if !reflect.DeepEqual(runtime.calls, []string{"model:custom/chosen", "auth:custom"}) {
				t.Fatalf("saved selection calls=%v", runtime.calls)
			}
		})
	}
}

// upstream: packages/coding-agent/src/core/model-resolver.ts:653-666. Initial CLI selection uses the shared resolver but returns the builtin thinking default even for a parsed suffix.
func TestFindInitialModelCLIUsesSharedResolution(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, provider, model, wantID, wantError string
	}{
		{name: "custom model suffix", provider: "custom", model: "custom/new:high", wantID: "new"},
		{name: "existing model suffix", provider: "custom", model: "base:high", wantID: "base"},
		{name: "unknown provider", provider: "missing", model: "base", wantError: `Unknown provider "missing". Use --list-models to see available providers/models.`},
	} {
		t.Run(row.name, func(t *testing.T) {
			runtime := &initialModelTestRuntime{models: []RuntimeModel{{Provider: "custom", ID: "base", Name: "Base"}}}
			got, err := FindInitialModel(FindInitialModelOptions{ModelRuntime: runtime, CLIProvider: row.provider, CLIModel: row.model, DefaultThinkingLevel: "low"})
			if row.wantError != "" {
				if err == nil || err.Error() != row.wantError || !reflect.DeepEqual(got, InitialModelResult{}) {
					t.Fatalf("result=%+v error=%v, want zero result and %q", got, err, row.wantError)
				}
			} else if err != nil || got.Model == nil || got.Model.Provider != "custom" || got.Model.ID != row.wantID || got.ThinkingLevel != DefaultThinkingLevel || got.FallbackMessage != "" {
				t.Fatalf("result=%+v error=%v, want custom/%s:%s", got, err, row.wantID, DefaultThinkingLevel)
			}
			if !reflect.DeepEqual(runtime.calls, []string{"models:"}) {
				t.Fatalf("CLI selection read auth or availability: %v", runtime.calls)
			}
		})
	}
}
