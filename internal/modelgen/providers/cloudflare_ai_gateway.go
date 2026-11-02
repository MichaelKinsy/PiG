package providers

// Ports packages/ai/catalog/providers/cloudflare-ai-gateway.ts

import (
	"strings"

	. "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts
)

// Copied from packages/ai/src/api/cloudflare.ts.
const (
	// AI Gateway Unified API. https://developers.cloudflare.com/ai-gateway/usage/unified-api/
	cloudflareAiGatewayCompatBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/compat"
	// AI Gateway → OpenAI passthrough. Used until /compat supports /v1/responses.
	cloudflareAiGatewayOpenAIBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/openai"
	// AI Gateway → Anthropic passthrough.
	cloudflareAiGatewayAnthropicBaseURL = "https://gateway.ai.cloudflare.com/v1/{CLOUDFLARE_ACCOUNT_ID}/{CLOUDFLARE_GATEWAY_ID}/anthropic"
)

// cloudflareAiGatewayRouteModel: models.dev lists gateway models as `<upstream>/<id>`; route each upstream to its passthrough.
func cloudflareAiGatewayRouteModel(model ChatModel) ChatModel {
	id := model.Str("id")
	before, after, ok := strings.Cut(id, "/")
	if !ok {
		return nil
	}
	upstream := before
	nativeID := after
	switch upstream {
	case "openai":
		// Cloudflare AI Gateway passes OpenAI usage through at OpenAI list prices.
		standardCost := OpenAIStandardCosts()[nativeID]
		result := Spread(model).
			Set("id", nativeID).
			Set("api", "openai-responses").
			Set("baseUrl", cloudflareAiGatewayOpenAIBaseURL)
		if standardCost != nil {
			result.Set("cost", WithOpenAILongContextPricing(standardCost))
		}
		return result
	case "anthropic":
		// The /anthropic passthrough forwards the model ID to Anthropic unchanged.
		// models.dev lists dotted versions (claude-opus-5.5), but Anthropic only
		// accepts dashed IDs (claude-opus-5-5).
		return Spread(model).
			Set("id", strings.ReplaceAll(nativeID, ".", "-")).
			Set("api", "anthropic-messages").
			Set("baseUrl", cloudflareAiGatewayAnthropicBaseURL).
			// Gateway passthroughs forward session affinity headers to upstreams that
			// use them for cache/routing affinity.
			Set("compat", O("sendSessionAffinityHeaders", true))
	case "workers-ai":
		return Spread(model).Set("compat", O("sendSessionAffinityHeaders", true))
	}
	return nil
}

func cloudflareAiGateway() ProviderDefinition {
	return ProviderDefinition{
		ID: "cloudflare-ai-gateway",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			models, err := ModelsDevModels(ctx, "cloudflare-ai-gateway", ModelsDevOptions{
				API:     "openai-completions",
				BaseURL: cloudflareAiGatewayCompatBaseURL,
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					return cloudflareAiGatewayRouteModel(model), nil
				},
			})
			if err != nil {
				return nil, err
			}
			// The gateway proxies Workers AI through its OpenAI-compatible /compat endpoint,
			// but models.dev may omit or intermittently drop those `workers-ai/*` entries
			// from the AI Gateway catalog. Mirror the Workers AI catalog under the documented
			// prefix so the gateway keeps its OpenAI-compatible models stable.
			ids := map[string]bool{}
			for _, model := range models {
				ids[model.Str("id")] = true
			}
			mirrored, err := ModelsDevModels(ctx, "cloudflare-workers-ai", ModelsDevOptions{
				API:     "openai-completions",
				BaseURL: cloudflareAiGatewayCompatBaseURL,
				Compat:  O("sendSessionAffinityHeaders", true),
				Include: func(source *ModelsDevModel) bool { return !ids["workers-ai/"+source.ID] },
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					return Spread(model).Set("id", "workers-ai/"+model.Str("id")), nil
				},
			})
			if err != nil {
				return nil, err
			}
			return append(models, mirrored...), nil
		},
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsDeveloperRole", false,
			"supportsReasoningEffort", false,
			"maxTokensField", "max_tokens",
			"supportsLongCacheRetention", false,
		))},
		Finalize: func(model ChatModel, _ *ProviderContext) {
			if model.Str("api") == "openai-responses" {
				MergeCompat(model, O("supportsStrictMode", true))
			}
			EnableOpenAIGrammarTools(model)
		},
	}
}
