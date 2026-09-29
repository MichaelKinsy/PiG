package main

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
		result := ResolveCliModel("", "neuralwatt/zai-org/GLM-5.1-FP8:high", "", runtime)
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
	ambiguous := []codingagent.RuntimeModel{{Provider: "azure-openai-responses", ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol"}, {Provider: "openai-codex", ID: "gpt-5.6-sol", Name: "GPT 5.6 Sol"}}
	cases := []cliCase{
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:322
		{name: "resolves --model provider/id without --provider", model: "openai/gpt-4o", wantProvider: "openai", wantID: "gpt-4o", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:337
		{name: "resolves fuzzy patterns within an explicit provider", provider: "openai", model: "4o", wantProvider: "openai", wantID: "gpt-4o", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:353
		{name: "supports --model <pattern>:<thinking> (without explicit --thinking)", model: "sonnet:high", wantID: "claude-sonnet-4-5", wantThinking: "high", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:368
		{name: "prefers exact model id match over provider inference (OpenRouter-style ids)", model: "openai/gpt-4o:extended", wantProvider: "openrouter", wantID: "openai/gpt-4o:extended", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:383
		{name: "does not strip invalid :suffix as thinking level in --model (treat as raw id)", provider: "openai", model: "gpt-4o:extended", wantProvider: "openai", wantID: "gpt-4o:extended", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:399
		{name: "allows custom model ids for explicit providers without double prefixing", provider: "openrouter", model: "openrouter/openai/ghost-model", wantProvider: "openrouter", wantID: "openai/ghost-model", models: all},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:415
		{name: "returns a clear error when there are no models", provider: "openai", model: "gpt-4o", errorParts: []string{"No models available"}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:430
		{name: "prefers the sole authenticated provider for an ambiguous bare exact model id", model: "gpt-5.6-sol", models: ambiguous, auth: "openai-codex", wantProvider: "openai-codex", wantID: "gpt-5.6-sol"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:458
		{name: "requires an explicit provider for an ambiguous bare exact model id without a unique authenticated provider", model: "gpt-5.6-sol", models: ambiguous, auth: "none", errorParts: []string{`Model "gpt-5.6-sol" is ambiguous across providers`, "azure-openai-responses/gpt-5.6-sol", "openai-codex/gpt-5.6-sol", "Use --provider or provider/model"}},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:488
		{name: "prefers provider/model split over gateway model with matching id", model: "zai/glm-5", models: append(upstreamResolverModels(), codingagent.RuntimeModel{Provider: "zai", ID: "glm-5", Name: "GLM-5", Reasoning: true}, codingagent.RuntimeModel{Provider: "vercel-ai-gateway", ID: "zai/glm-5", Name: "GLM-5", Reasoning: true}), wantProvider: "zai", wantID: "glm-5"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:530
		{name: "prefers an authenticated exact raw model id over an unauthenticated inferred provider", model: "xiaomi/mimo-v2.5-pro", models: append(upstreamResolverModels(), codingagent.RuntimeModel{Provider: "commandcode", ID: "xiaomi/mimo-v2.5-pro", Name: "Xiaomi MiMo via Commandcode"}, codingagent.RuntimeModel{Provider: "xiaomi", ID: "mimo-v2.5-pro", Name: "Xiaomi MiMo"}), auth: "commandcode", wantProvider: "commandcode", wantID: "xiaomi/mimo-v2.5-pro"},
		// .upstream/v0.87.1/packages/coding-agent/test/model-resolver.test.ts:570
		{name: "resolves provider-prefixed fuzzy patterns (openrouter/qwen -> openrouter model)", model: "openrouter/qwen", models: all, wantProvider: "openrouter", wantID: "qwen/qwen3-coder:exacto"},
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
			got := ResolveCliModel(tc.provider, tc.model, tc.thinking, runtime)
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
			if got.Model.ID != tc.wantID || (tc.wantProvider != "" && got.Model.Provider != tc.wantProvider) || got.ThinkingLevel != tc.wantThinking {
				t.Errorf("model=%+v thinking=%q", got.Model, got.ThinkingLevel)
			}
			if tc.reasoning && !got.Model.Reasoning {
				t.Error("custom model did not enable reasoning requested by its thinking suffix")
			}
		})
	}
}
