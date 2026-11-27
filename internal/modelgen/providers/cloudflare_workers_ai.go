package providers

// Ports packages/ai/catalog/providers/cloudflare-workers-ai.ts

import . "github.com/MichaelKinsy/PiG/internal/modelgen" //nolint:staticcheck // ST1001: provider files mirror packages/ai/catalog/providers; the dot import keeps the definition helpers (O, Spread, MergeCompat, ...) as unqualified as the TypeScript imports of ../define.ts

// Copied from packages/ai/src/api/cloudflare.ts.
const (
	// Workers AI direct endpoint.
	cloudflareWorkersAiBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	// Workers AI REST endpoint root; model runs are posted to `run`.
	cloudflareWorkersAiRestBaseURL = "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai"
)

func cloudflareWorkersAiClassifier(id, name string, input, contextWindow float64, imageInput string) *Obj {
	return O(
		"type", "classifier",
		"id", id,
		"name", name,
		"api", "cloudflare-workers-ai-system-one",
		"provider", "cloudflare-workers-ai",
		"baseUrl", cloudflareWorkersAiRestBaseURL,
		"input", Strings("text", "image"),
		"cost", O("input", input, "output", 0, "cacheRead", 0, "cacheWrite", 0),
		"contextWindow", contextWindow,
		"compat", O("imageInput", imageInput),
	)
}

// Workers AI has no unauthenticated catalog, so its System One models are listed here from
// Cloudflare's model pages.
func cloudflareWorkersAiClassifierModels() []*Obj {
	return []*Obj{
		// Cloudflare-hosted Clef decision models. They take up to four PNG, JPEG, or WebP images as data URLs.
		// Clef Omni also takes audio and video, which classifier contexts do not carry.
		// Pricing and context: https://developers.cloudflare.com/workers-ai/models/clef/,
		// https://developers.cloudflare.com/workers-ai/models/clef-flash/,
		// and https://developers.cloudflare.com/workers-ai/models/clef-omni/
		cloudflareWorkersAiClassifier("@cf/cloudflare/clef", "Clef", 0.24, 65536, "images"),
		cloudflareWorkersAiClassifier("@cf/cloudflare/clef-flash", "Clef Flash", 0.038, 24576, "images"),
		cloudflareWorkersAiClassifier("@cf/cloudflare/clef-omni", "Clef Omni", 0.15, 64000, "images"),
		// TypeSafe's Jev models take PNG and JPEG images as System One assets.
		// https://developers.cloudflare.com/ai/models/typesafe/jev/ (jev-latest)
		// and https://developers.cloudflare.com/ai/models/typesafe/jev-1.14.0/
		cloudflareWorkersAiClassifier("typesafe/jev", "Jev", 0.042, 32000, "assets"),
		cloudflareWorkersAiClassifier("typesafe/jev-1.14.0", "Jev 1.14", 0.042, 32000, "assets"),
	}
}

// These models reject null content when an assistant message contains only tool calls.
var cloudflareWorkersAiNonNullAssistantContentModels = map[string]bool{
	"@cf/ibm-granite/granite-4.0-h-micro":      true,
	"@cf/meta/llama-3.3-70b-instruct-fp8-fast": true,
	"@cf/openai/gpt-oss-120b":                  true,
	"@cf/openai/gpt-oss-20b":                   true,
}

func cloudflareWorkersAi() ProviderDefinition {
	return ProviderDefinition{
		ID: "cloudflare-workers-ai",
		Models: func(ctx *ProviderContext) ([]ChatModel, error) {
			return ModelsDevModels(ctx, "cloudflare-workers-ai", ModelsDevOptions{
				API:     "openai-completions",
				BaseURL: cloudflareWorkersAiBaseURL,
				Compat:  O("sendSessionAffinityHeaders", true),
				Map: func(model ChatModel, _ *ModelsDevModel) (ChatModel, error) {
					if cloudflareWorkersAiNonNullAssistantContentModels[model.Str("id")] {
						MergeCompat(model, O("requiresNonNullAssistantContent", true))
					}
					return model, nil
				},
			})
		},
		Classifiers: func(*ProviderContext) ([]*Obj, error) { return cloudflareWorkersAiClassifierModels(), nil },
		Compat: ApiCompatDefaults{"openai-completions": FixedCompat(O(
			"supportsStore", false,
			"supportsStrictMode", true,
			"supportsLongCacheRetention", false,
		))},
	}
}
