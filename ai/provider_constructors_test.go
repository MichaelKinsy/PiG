package ai

import (
	"context"
	"slices"
	"testing"
)

// Pi packages/ai/src/providers/<id>.ts: every per-provider constructor returns its provider id with the catalog's models.
func TestProviderConstructorsMatchCatalog(t *testing.T) {
	constructors := map[string]func() *ModelsProvider{
		"amazon-bedrock": AmazonBedrockProvider, "ant-ling": AntLingProvider, "anthropic": AnthropicProvider, "azure": AzureProvider,
		"baseten": BasetenProvider, "cerebras": CerebrasProvider, "cloudflare-ai-gateway": CloudflareAIGatewayProvider,
		"deepseek": DeepseekProvider, "fireworks": FireworksProvider, "github-copilot": GithubCopilotProvider, "google": GoogleProvider,
		"google-vertex": GoogleVertexProvider, "groq": GroqProvider, "huggingface": HuggingFaceProvider, "kimi-coding": KimiCodingProvider,
		"meta": MetaProvider, "minimax": MinimaxProvider, "minimax-cn": MinimaxCnProvider, "mistral": MistralProvider,
		"moonshotai": MoonshotaiProvider, "moonshotai-cn": MoonshotaiCnProvider, "nvidia": NvidiaProvider, "openai": OpenaiProvider,
		"openai-codex": OpenaiCodexProvider, "opencode": OpencodeProvider, "opencode-go": OpenCodeGoProvider, "openrouter": OpenrouterProvider,
		"qwen-token-plan": QwenTokenPlanProvider, "qwen-token-plan-cn": QwenTokenPlanCnProvider, "qwen-token-plan-individual": QwenTokenPlanIndividualProvider,
		"together": TogetherProvider, "vercel-ai-gateway": VercelAIGatewayProvider, "xai": XaiProvider, "xiaomi": XiaomiProvider,
		"xiaomi-token-plan-ams": XiaomiTokenPlanAmsProvider, "xiaomi-token-plan-cn": XiaomiTokenPlanCnProvider, "xiaomi-token-plan-sgp": XiaomiTokenPlanSgpProvider,
		"zai": ZaiProvider, "zai-coding-cn": ZaiCodingCnProvider,
	}
	known := ListProviders()
	for id, construct := range constructors {
		if !slices.Contains(known, id) {
			t.Errorf("%s is not a catalog provider", id)
			continue
		}
		provider := construct()
		if provider.ID != id {
			t.Errorf("%s: constructor returned provider %q", id, provider.ID)
		}
		models, err := provider.GetAllModels()
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got, want := len(models), len(GetAllBuiltinModels(id)); got != want {
			t.Errorf("%s: %d models, want %d", id, got, want)
		}
	}
}

// Pi providers/opencode.ts and opencode-go.ts wrap their API streams with withOpenCodeSessionHeader; other providers do not.
func TestOpenCodeProvidersAddSessionHeaderToStreams(t *testing.T) {
	for id, wrapped := range map[string]bool{"opencode": true, "opencode-go": true, "groq": false, "anthropic": false} {
		options := withBuiltinStreamWrapper(id, &ProviderStreams{StreamSimple: func(_ context.Context, _ *Model, _ TranscriptContext, options StreamOptions) (*AssistantMessageEventStream, error) {
			if got := options.Headers[openCodeSessionHeader] != nil; got != wrapped {
				t.Errorf("%s: session header present = %v, want %v", id, got, wrapped)
			}
			return nil, nil
		}})
		if _, err := options.StreamSimple(context.Background(), nil, TranscriptContext{}, StreamOptions{SessionID: "s"}); err != nil {
			t.Fatal(err)
		}
	}
}

// providers/all.ts: every catalog provider is built by its own constructor; BuiltinProviders agrees with the constructor table for each id and serves the whole catalog.
func TestBuiltinProvidersAreBuiltByTheirConstructors(t *testing.T) {
	built := BuiltinProviders()
	ids := ListProviders()
	if len(built) != len(ids) {
		t.Fatalf("%d providers for %d catalog ids", len(built), len(ids))
	}
	for i, provider := range built {
		if provider.ID != ids[i] {
			t.Errorf("position %d: provider %q, catalog id %q", i, provider.ID, ids[i])
		}
		if _, ok := builtinProviderConstructors[ids[i]]; !ok {
			t.Errorf("catalog provider %q has no constructor", ids[i])
		}
	}
}
