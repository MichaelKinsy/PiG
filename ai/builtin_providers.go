package ai

// Ports packages/ai/src/providers/all.ts (built-in model accessors and provider assembly), providers/typesafe.ts,
// providers/cloudflare-workers-ai.ts and the classifier registrations of providers/{opencode,openrouter,
// vercel-ai-gateway}.ts.

import (
	"slices"
)

// GetBuiltinClassifierModels lists the built-in classifier models of one provider, in catalog order. The models are
// copies: changing one does not change the catalog.
func GetBuiltinClassifierModels(provider string) []*ClassifierModel {
	var models []*ClassifierModel
	for i := range GeneratedClassifierModels {
		if GeneratedClassifierModels[i].Provider == provider {
			models = append(models, cloneClassifierModel(&GeneratedClassifierModels[i]))
		}
	}
	return models
}

// GetBuiltinClassifierModel reads one built-in classifier model, or nil.
func GetBuiltinClassifierModel(provider, id string) *ClassifierModel {
	for i := range GeneratedClassifierModels {
		if model := &GeneratedClassifierModels[i]; model.Provider == provider && model.ID == id {
			return cloneClassifierModel(model)
		}
	}
	return nil
}

func cloneClassifierModel(model *ClassifierModel) *ClassifierModel {
	clone := *model
	clone.Headers = cloneStringMap(model.Headers)
	clone.Input = slices.Clone(model.Input)
	return &clone
}

// GetBuiltinModels lists the built-in chat models of one provider.
func GetBuiltinModels(provider string) []*Model {
	if provider == "" {
		return nil
	}
	var models []*Model
	for _, generated := range ListModels(provider) {
		models = append(models, generated.ToModel())
	}
	return models
}

// GetAllBuiltinModels lists the built-in chat, image and classifier models of one provider.
func GetAllBuiltinModels(provider string) []AnyModel {
	var models []AnyModel
	for _, model := range GetBuiltinModels(provider) {
		models = append(models, model)
	}
	for _, image := range GetImageModels(BuiltinImageProvider(provider)) {
		models = append(models, &image)
	}
	for _, classifier := range GetBuiltinClassifierModels(provider) {
		models = append(models, classifier)
	}
	return models
}

// builtinClassifiers are the classifier implementations of the providers that serve classifier models.
func builtinClassifiers(provider string) ProviderClassifierMap {
	switch provider {
	case "typesafe", "opencode", "openrouter", "vercel-ai-gateway":
		return ProviderClassifierMap{ClassifierAPITypesafeSystemOne: TypesafeSystemOneAPI()}
	case "cloudflare-workers-ai":
		return ProviderClassifierMap{ClassifierAPICloudflareWorkersAISystemOne: CloudflareClassifier(CloudflareWorkersAISystemOneAPI())}
	case "openai":
		return ProviderClassifierMap{ClassifierAPIOpenAIDecisions: OpenAIDecisionsAPI()}
	}
	return nil
}

// builtinFilterAllModels are the availability policies of the providers that hide models for some credentials
// (providers/openai.ts filterAllModels).
var builtinFilterAllModels = map[string]func([]AnyModel, *Credential) []AnyModel{
	// Sign in with ChatGPT tokens only reach the Responses API; the Decisions API rejects them.
	"openai": func(models []AnyModel, credential *Credential) []AnyModel {
		if credential == nil || credential.Type != CredentialOAuth {
			return models
		}
		return slices.DeleteFunc(slices.Clone(models), func(model AnyModel) bool { return IsModelType(model, ModelTypeClassifier) })
	},
}

// builtinStreamWrappers are the providers whose upstream constructor wraps its API streams (providers/opencode.ts, opencode-go.ts: withOpenCodeSessionHeader).
var builtinStreamWrappers = map[string]func(*ProviderStreams) *ProviderStreams{
	"opencode":    WithOpenCodeSessionHeader,
	"opencode-go": WithOpenCodeSessionHeader,
	// providers/cloudflare-workers-ai.ts:20 and cloudflare-ai-gateway.ts:22-24 wrap their API streams with cloudflareStreams.
	"cloudflare-workers-ai": CloudflareStreams,
	"cloudflare-ai-gateway": CloudflareStreams,
}

func withBuiltinStreamWrapper(id string, streams *ProviderStreams) *ProviderStreams {
	if wrap := builtinStreamWrappers[id]; wrap != nil {
		return wrap(streams)
	}
	return streams
}

// builtinProvider assembles one built-in provider: its chat models routed through the registered API implementations,
// its image models and its classifier models.
func builtinProvider(id string) *ModelsProvider {
	auth, err := BuiltinProviderAuth(id)
	if err != nil {
		panic(err)
	}
	options := CreateProviderOptions{ID: id, Name: new(ProviderDisplayName(id)), BaseURL: builtinProviderBaseURLs[id], Auth: auth, Models: GetAllBuiltinModels(id), FilterAllModels: builtinFilterAllModels[id], Classifiers: builtinClassifiers(id)}
	if id == "github-copilot" {
		// upstream: packages/ai/src/providers/github-copilot.ts:githubCopilotProvider filterModels.
		options.FilterModels = filterGitHubCopilotModels
	}
	if len(GetBuiltinModels(id)) > 0 {
		options.API = withBuiltinStreamWrapper(id, &ProviderStreams{Stream: StreamSimple, StreamSimple: StreamSimple})
	}
	if len(GetImageModels(BuiltinImageProvider(id))) > 0 {
		options.Images = ProviderImageAPIMap{APIImagesOpenRouter: OpenRouterImagesAPI()}
	}
	return CreateProvider(options)
}

// TypesafeProvider is the TypeSafe provider: classifier models only.
func TypesafeProvider() *ModelsProvider { return builtinProvider("typesafe") }

// CloudflareWorkersAIProvider is the Cloudflare Workers AI provider.
func CloudflareWorkersAIProvider() *ModelsProvider { return builtinProvider("cloudflare-workers-ai") }

// builtinProviderConstructors lists the per-provider constructors (providers/all.ts builtinModels): BuiltinProviders builds each provider through its own constructor.
var builtinProviderConstructors = map[string]func() *ModelsProvider{
	"all":                        BuiltinProvider,
	"amazon-bedrock":             AmazonBedrockProvider,
	"ant-ling":                   AntLingProvider,
	"anthropic":                  AnthropicProvider,
	"azure":                      AzureProvider,
	"baseten":                    BasetenProvider,
	"cerebras":                   CerebrasProvider,
	"cloudflare-ai-gateway":      CloudflareAIGatewayProvider,
	"deepseek":                   DeepseekProvider,
	"fireworks":                  FireworksProvider,
	"github-copilot":             GithubCopilotProvider,
	"google":                     GoogleProvider,
	"google-vertex":              GoogleVertexProvider,
	"groq":                       GroqProvider,
	"huggingface":                HuggingFaceProvider,
	"kimi-coding":                KimiCodingProvider,
	"meta":                       MetaProvider,
	"minimax":                    MinimaxProvider,
	"minimax-cn":                 MinimaxCnProvider,
	"mistral":                    MistralProvider,
	"moonshotai":                 MoonshotaiProvider,
	"moonshotai-cn":              MoonshotaiCnProvider,
	"nvidia":                     NvidiaProvider,
	"openai":                     OpenaiProvider,
	"openai-codex":               OpenaiCodexProvider,
	"opencode":                   OpencodeProvider,
	"opencode-go":                OpenCodeGoProvider,
	"openrouter":                 OpenrouterProvider,
	"qwen-token-plan":            QwenTokenPlanProvider,
	"qwen-token-plan-cn":         QwenTokenPlanCnProvider,
	"qwen-token-plan-individual": QwenTokenPlanIndividualProvider,
	"radius":                     func() *ModelsProvider { return NewRadiusProvider() }, // all.ts:169 radiusProvider()
	"together":                   TogetherProvider,
	"vercel-ai-gateway":          VercelAIGatewayProvider,
	"xai":                        XaiProvider,
	"xiaomi":                     XiaomiProvider,
	"xiaomi-token-plan-ams":      XiaomiTokenPlanAmsProvider,
	"xiaomi-token-plan-cn":       XiaomiTokenPlanCnProvider,
	"xiaomi-token-plan-sgp":      XiaomiTokenPlanSgpProvider,
	"zai":                        ZaiProvider,
	"zai-coding-cn":              ZaiCodingCnProvider,
	"typesafe":                   TypesafeProvider,
	"cloudflare-workers-ai":      CloudflareWorkersAIProvider,
}

// BuiltinProviders returns every built-in provider, freshly constructed.
func BuiltinProviders() []*ModelsProvider {
	ids := ListProviders()
	providers := make([]*ModelsProvider, len(ids))
	for i, id := range ids {
		if construct, ok := builtinProviderConstructors[id]; ok {
			providers[i] = construct()
			continue
		}
		providers[i] = builtinProvider(id)
	}
	return providers
}

// BuiltinModels returns a Models collection with every built-in provider registered.
func BuiltinModels(options ...CreateModelsOptions) *Models {
	models := CreateModels(options...)
	for _, provider := range BuiltinProviders() {
		models.SetProvider(provider)
	}
	return models
}

// HuggingFaceProvider is the Hugging Face provider (providers/huggingface.ts huggingfaceProvider).
func HuggingFaceProvider() *ModelsProvider { return builtinProvider("huggingface") }

// OpenCodeGoProvider is the OpenCode Go provider (providers/opencode-go.ts opencodeGoProvider).
func OpenCodeGoProvider() *ModelsProvider { return builtinProvider("opencode-go") }

// XiaomiProvider is the Xiaomi provider (providers/xiaomi.ts xiaomiProvider).
func XiaomiProvider() *ModelsProvider { return builtinProvider("xiaomi") }
