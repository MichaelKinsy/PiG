package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/src/core/model-resolver.ts:175-190,574-590. Custom IDs copy the complete selected base model; reasoning promotion changes that copy, not the catalog object.
func TestResolveCliModelRetainsNativeModel(t *testing.T) {
	t.Parallel()
	for _, api := range []ai.API{"openai-completions", "openai-responses"} {
		for _, row := range []struct {
			name, pattern, cliThinking, id, thinking string
			custom, reasoning                        bool
		}{
			{name: "exact", pattern: "base", id: "base"},
			{name: "custom", pattern: "custom", id: "custom", custom: true},
			{name: "custom suffix", pattern: "custom:high", id: "custom", thinking: "high", custom: true, reasoning: true},
			{name: "custom off", pattern: "custom:off", id: "custom", thinking: "off", custom: true},
			{name: "explicit thinking keeps suffix", pattern: "custom:high", cliThinking: "low", id: "custom:high", custom: true, reasoning: true},
			{name: "invalid suffix retained", pattern: "custom:unknown", id: "custom:unknown", custom: true},
		} {
			t.Run(string(api)+"/"+row.name, func(t *testing.T) {
				native := &ai.Model{
					ID: "base", DisplayName: "Catalog name", Provider: &cycleTestProvider{id: "native-custom"},
					ProviderMeta: ai.ProviderMetadata{ProviderID: "native-custom", API: api, BaseURL: "https://catalog.invalid/v1", Headers: map[string]string{"X-Catalog": "retained"}, Compat: &ai.ModelCompat{SupportsDeveloperRole: new(false), SupportsStore: new(false)}},
					Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingLevelLow, SupportsImages: true, SupportsToolUse: true, ContextWindow: 234567, MaxOutputTokens: 4567, InputCostPer1M: 1.25, OutputCostPer1M: 3.5, CacheReadCostPer1M: 0.75, CacheWriteCostPer1M: 2, CostTiers: []ai.CostTier{{InputTokensAbove: 45678, InputCostPer1M: 2.5}}},
					Input:        []string{"text", "image"}, ThinkingLevelMap: ai.ThinkingLevelMap{"high": new("native-high")},
					SamplingParams: map[string]any{"top_p": 0.73}, PromptCache: ai.ModelPromptCache{"ttl": 120}, InputLimits: &ai.ModelInputLimits{MaxRequestBytes: 98765},
				}
				before := *native
				metadata := RuntimeModel{NativeModel: native, Provider: "native-custom", ID: "base", Name: "Catalog name"}
				runtime := &initialModelTestRuntime{models: []RuntimeModel{metadata}}
				got := ResolveCliModel("native-custom", row.pattern, row.cliThinking, runtime)
				if got.Error != "" || got.Model == nil || got.Model.NativeModel == nil || got.Model.ID != row.id || string(got.ThinkingLevel) != row.thinking || got.Model.Reasoning != row.reasoning {
					t.Fatalf("selection=%+v model=%+v", got, got.Model)
				}
				want := before
				if row.custom {
					want.ID, want.DisplayName = row.id, row.id
					want.ProviderMeta.Reasoning = row.reasoning
					if got.Model.NativeModel == native {
						t.Fatal("custom selection retained the original model object")
					}
				} else if got.Model.NativeModel != native {
					t.Fatal("ordinary selection replaced native model identity")
				}
				if !reflect.DeepEqual(*got.Model.NativeModel, want) || got.Model.NativeModel.Provider != native.Provider {
					t.Fatalf("native model=%+v, want complete source model %+v", *got.Model.NativeModel, want)
				}
				if !reflect.DeepEqual(*native, before) || !reflect.DeepEqual(runtime.models[0], metadata) {
					t.Fatal("selection mutated its original native model or metadata")
				}
			})
		}
	}
}

// The native attachment is Go-only representation state. It must neither change metadata JSON nor force metadata-only CLI callers onto a native reconstruction path.
func TestRuntimeModelNativeAttachmentIsNonWireAndOptional(t *testing.T) {
	t.Parallel()
	metadata := RuntimeModel{Provider: "custom", ID: "base", Name: "Base"}
	before, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	metadata.NativeModel = &ai.Model{ID: "base", SamplingParams: map[string]any{"not-a-JSON-callback": func() {}}}
	after, err := json.Marshal(metadata)
	if err != nil || string(after) != string(before) {
		t.Fatalf("metadata JSON=%s error=%v, want %s without native data", after, err, before)
	}
	metadata.NativeModel = nil
	runtime := &initialModelTestRuntime{models: []RuntimeModel{metadata}}
	got := ResolveCliModel("custom", "new:high", "", runtime)
	if got.Error != "" || got.Model == nil || got.Model.NativeModel != nil || got.Model.ID != "new" || !got.Model.Reasoning || got.ThinkingLevel != "high" {
		t.Fatalf("metadata-only selection changed: %+v model=%+v", got, got.Model)
	}
}

// upstream: packages/coding-agent/src/core/model-resolver.ts:669-720. Initial selection preserves the native pointer supplied by either the available snapshot, saved-model lookup or scope.
func TestFindInitialModelRetainsNativeIdentity(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"available", "saved", "scope"} {
		t.Run(path, func(t *testing.T) {
			native := &ai.Model{ID: "chosen", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom", API: "openai-responses", BaseURL: "https://selected.invalid"}}
			metadata := RuntimeModel{NativeModel: native, Provider: "custom", ID: "chosen"}
			runtime := &initialModelTestRuntime{models: []RuntimeModel{metadata}, available: []RuntimeModel{metadata}, configured: map[string]bool{"custom": true}}
			options := FindInitialModelOptions{ModelRuntime: runtime}
			switch path {
			case "saved":
				options.DefaultProvider, options.DefaultModelId = "custom", "chosen"
			case "scope":
				options.ScopedModels = []ScopedModel{{Model: metadata}}
			}
			got, err := FindInitialModel(options)
			if err != nil || got.Model == nil || got.Model.NativeModel != native {
				t.Fatalf("initial model=%+v error=%v lost native pointer %p", got.Model, err, native)
			}
		})
	}
}
