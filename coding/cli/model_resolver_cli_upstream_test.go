package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func BenchmarkResolveCustomModelThinking(b *testing.B) {
	models := make([]codingagent.RuntimeModel, 1000)
	for i := range models {
		models[i] = codingagent.RuntimeModel{Provider: "neuralwatt", ID: fmt.Sprintf("model-%d", i), Name: "Model"}
	}
	runtime := newStartupModelRuntime(models, func(string) bool { return true })
	b.ReportAllocs()
	for b.Loop() {
		result := ResolveCliModel("", "neuralwatt/zai-org/GLM-5.1-FP8:high", "", false, runtime)
		if result.Model == nil || !result.Model.Reasoning {
			b.Fatal("custom reasoning resolution failed")
		}
	}
}

func TestModelResolverCLIUpstream(t *testing.T) {
	type cliCase struct {
		name, provider, model, thinking, wantProvider, wantID, wantThinking, auth string
		models                                                                    []codingagent.RuntimeModel
		errorParts                                                                []string
		reasoning                                                                 bool
	}
	all := upstreamResolverModels()
	cases := []cliCase{
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:383
		{name: "does not strip invalid :suffix as thinking level in --model (treat as raw id)", provider: "openai", model: "gpt-4o:extended", wantProvider: "openai", wantID: "gpt-4o:extended", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:399
		{name: "allows custom model ids for explicit providers without double prefixing", provider: "openrouter", model: "openrouter/openai/ghost-model", wantProvider: "openrouter", wantID: "openai/ghost-model", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:415
		{name: "returns a clear error when there are no models", provider: "openai", model: "gpt-4o", errorParts: []string{"No models available"}},
	}
	neuralwatt := append(upstreamResolverModels(), codingagent.RuntimeModel{Provider: "neuralwatt", ID: "some-base-model", Name: "Some Base Model", Reasoning: false})
	cases = append(cases, []cliCase{
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:603
		{name: "strips :thinking suffix from custom model id in fallback path", model: "neuralwatt/zai-org/GLM-5.1-FP8:high", models: neuralwatt, wantProvider: "neuralwatt", wantID: "zai-org/GLM-5.1-FP8", wantThinking: "high", reasoning: true},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:621
		{name: "custom model without thinking suffix works normally in fallback path", model: "neuralwatt/zai-org/GLM-5.1-FP8", models: neuralwatt, wantProvider: "neuralwatt", wantID: "zai-org/GLM-5.1-FP8"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:654
		{name: "invalid thinking suffix on custom model is treated as part of model id", model: "neuralwatt/zai-org/GLM-5.1-FP8:banana", models: neuralwatt, wantProvider: "neuralwatt", wantID: "zai-org/GLM-5.1-FP8:banana"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:671
		{name: "explicit --provider with custom model:thinking strips suffix correctly", provider: "neuralwatt", model: "zai-org/GLM-5.1-FP8:high", models: neuralwatt, wantProvider: "neuralwatt", wantID: "zai-org/GLM-5.1-FP8", wantThinking: "high"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:688
		{name: "with explicit --thinking, :suffix is kept as part of model id", model: "neuralwatt/zai-org/GLM-5.1-FP8:high", thinking: "medium", models: neuralwatt, wantProvider: "neuralwatt", wantID: "zai-org/GLM-5.1-FP8:high"},
	}...)
	// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:637
	for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		cases = append(cases, cliCase{name: "all valid thinking levels work in fallback path/" + level, model: "neuralwatt/zai-org/GLM-5.1-FP8:" + level, models: neuralwatt, wantID: "zai-org/GLM-5.1-FP8", wantThinking: level})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := newStartupModelRuntime(tc.models, func(provider string) bool { return tc.auth == "" || provider == tc.auth })
			got := ResolveCliModel(tc.provider, tc.model, tc.thinking, false, runtime)
			if len(tc.errorParts) > 0 {
				if got.Model != nil {
					t.Errorf("model=%+v on error", got.Model)
				}
				for _, part := range tc.errorParts {
					if !strings.Contains(got.Error, part) {
						t.Errorf("error=%q, missing %q", got.Error, part)
					}
				}
				return
			}
			if got.Error != "" || got.Model == nil {
				t.Fatalf("result=%+v", got)
			}
			if got.Model.ID != tc.wantID || (tc.wantProvider != "" && got.Model.Provider != tc.wantProvider) || string(got.ThinkingLevel) != tc.wantThinking {
				t.Errorf("model=%+v thinking=%q", got.Model, got.ThinkingLevel)
			}
			if tc.reasoning && !got.Model.Reasoning {
				t.Error("custom model did not enable reasoning requested by its thinking suffix")
			}
		})
	}
}
