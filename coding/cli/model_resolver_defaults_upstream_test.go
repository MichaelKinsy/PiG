package cli

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestModelResolverDefaultsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name string
		want map[string]string
	}{
		// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:709
		{"openai defaults track current models", map[string]string{"openai": "gpt-5.5", "openai-codex": "gpt-6.1-sol"}},
		// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:714
		{"zai, minimax, cerebras, and ant-ling defaults track current models", map[string]string{"zai": "glm-5.3", "zai-coding-cn": "glm-5.3", "minimax": "MiniMax-M2.7", "minimax-cn": "MiniMax-M2.7", "cerebras": "gpt-oss-120b", "ant-ling": "Ring-2.6-1T"}},
		// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:738
		{"ai-gateway default tracks current model", map[string]string{"vercel-ai-gateway": "zai/glm-5.1"}},
		// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:742
		{"xai default tracks current model", map[string]string{"xai": "grok-4.7"}},
		// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:746
		{"qwen token plan individual default tracks current model", map[string]string{"qwen-token-plan-individual": "qwen3.8-max"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for provider, want := range tc.want {
				if got := codingagent.DefaultModelPerProvider()[provider]; got != want {
					t.Errorf("%s default=%q, want %q", provider, got, want)
				}
			}
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:723-736: every built-in provider (getBuiltinProviders,
	// Object.keys(MODELS), which includes providers whose catalog has only image or classifier models) either has a default
	// in its chat catalog or, with no chat models, no default at all.
	t.Run("built-in chat providers have defaults in their generated catalogs", func(t *testing.T) {
		chatless := 0
		for _, provider := range ai.ListProviders() {
			t.Run(provider, func(t *testing.T) {
				id, hasDefault := codingagent.DefaultModelPerProvider()[provider]
				if len(ai.ListModels(provider)) == 0 {
					chatless++
					if hasDefault {
						t.Errorf("%s has no chat models and should have no chat default, got %q", provider, id)
					}
					return
				}
				if _, ok := ai.LookupModelExact(provider + "/" + id); !ok {
					t.Errorf("%s default %s is absent from generated catalog", provider, id)
				}
			})
		}
		if chatless == 0 {
			t.Error("the pinned catalog has a provider without chat models (typesafe); none was checked")
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:750
	t.Run("findInitialModel accepts explicit provider custom model ids", func(t *testing.T) {
		rt := newStartupModelRuntime(upstreamResolverModels(), func(string) bool { return true })
		got, err := codingagent.FindInitialModel(codingagent.FindInitialModelOptions{CLIProvider: "openrouter", CLIModel: "openrouter/openai/ghost-model", ModelRuntime: rt})
		if err != nil || got.Model == nil || got.Model.Provider != "openrouter" || got.Model.ID != "openai/ghost-model" {
			t.Fatalf("model=%+v, err=%v", got, err)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:767
	t.Run("findInitialModel selects ai-gateway default when available", func(t *testing.T) {
		rt := newStartupModelRuntime([]codingagent.RuntimeModel{{Provider: "vercel-ai-gateway", ID: "anthropic/claude-opus-4-6", Name: "Claude Opus 4.6", Reasoning: true}}, func(string) bool { return true })
		got, err := findInitialModel(rt, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Provider != "vercel-ai-gateway" || got.ID != "anthropic/claude-opus-4-6" {
			t.Fatalf("model=%+v", got)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/model-resolver.test.ts:795
	t.Run("findInitialModel ignores an unauthenticated saved default", func(t *testing.T) {
		rt := newStartupModelRuntime([]codingagent.RuntimeModel{{Provider: "deepseek", ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true}, {Provider: "spark-two", ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", Reasoning: true}}, func(provider string) bool { return provider == "spark-two" })
		got, err := findInitialModel(rt, "deepseek", "deepseek-v4-flash")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Provider != "spark-two" || got.ID != "deepseek-v4-flash" {
			t.Fatalf("model=%+v", got)
		}
	})
}
