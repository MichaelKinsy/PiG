package models

import (
	"errors"
	"fmt"
	"os"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/models/model-config.ts (adapted upstream from Pi, MIT): an immutable, credential-blind
// models.json snapshot. A provider is its JSON object as the file wrote it (key order kept), validated against the
// schema below; provider-config.ts composes providers from these objects.

var (
	str    = schema.Str
	num    = schema.Num
	boolOf = schema.Bool
	nulOf  = schema.Nul
	opt    = schema.Opt
	prop   = schema.Prop
)

func obj(properties schema.Properties) schema.Schema { return schema.Obj(properties, schema.Open()) }
func arr(item schema.Schema) schema.Schema           { return schema.Arr(item) }
func union(branches ...schema.Schema) schema.Schema  { return schema.Union(branches) }
func integer(options ...schema.NumberOption) schema.Schema {
	return schema.Int(options...)
}
func nonEmpty() schema.Schema { return str(schema.MinLength(1)) }
func literals(values ...string) schema.Schema {
	branches := make([]schema.Schema, len(values))
	for i, value := range values {
		branches[i] = p2runtime.Literal(value)
	}
	return union(branches...)
}

var (
	percentileCutoffsSchema = obj(schema.Properties{opt("p50", num()), opt("p75", num()), opt("p90", num()), opt("p99", num())})

	openRouterRoutingSchema = obj(schema.Properties{
		opt("allow_fallbacks", boolOf()),
		opt("require_parameters", boolOf()),
		opt("data_collection", literals("deny", "allow")),
		opt("zdr", boolOf()),
		opt("enforce_distillable_text", boolOf()),
		opt("order", arr(str())),
		opt("only", arr(str())),
		opt("ignore", arr(str())),
		opt("quantizations", arr(str())),
		opt("sort", union(str(), obj(schema.Properties{opt("by", str()), opt("partition", union(str(), nulOf()))}))),
		opt("max_price", obj(schema.Properties{
			opt("prompt", union(num(), str())),
			opt("completion", union(num(), str())),
			opt("image", union(num(), str())),
			opt("audio", union(num(), str())),
			opt("request", union(num(), str())),
		})),
		opt("preferred_min_throughput", union(num(), percentileCutoffsSchema)),
		opt("preferred_max_latency", union(num(), percentileCutoffsSchema)),
	})

	vercelGatewayRoutingSchema = obj(schema.Properties{opt("only", arr(str())), opt("order", arr(str()))})

	thinkingLevelMapValueSchema = union(str(), nulOf())
	thinkingLevelMapSchema      = obj(schema.Properties{
		opt("off", thinkingLevelMapValueSchema),
		opt("minimal", thinkingLevelMapValueSchema),
		opt("low", thinkingLevelMapValueSchema),
		opt("medium", thinkingLevelMapValueSchema),
		opt("high", thinkingLevelMapValueSchema),
		opt("xhigh", thinkingLevelMapValueSchema),
		opt("max", thinkingLevelMapValueSchema),
	})

	chatTemplateKwargSchema = union(
		str(), num(), boolOf(), nulOf(),
		obj(schema.Properties{
			prop("$var", literals("thinking.enabled", "thinking.effort")),
			opt("omitWhenOff", boolOf()),
		}),
	)

	sessionAffinityFormatSchema = literals("openai", "openai-nosession", "openrouter")

	openAICompletionsCompatSchema = obj(schema.Properties{
		opt("supportsStore", boolOf()),
		opt("supportsDeveloperRole", boolOf()),
		opt("supportsReasoningEffort", boolOf()),
		opt("supportsUsageInStreaming", boolOf()),
		opt("supportsFinishReason", boolOf()),
		opt("maxTokensField", literals("max_completion_tokens", "max_tokens")),
		opt("requiresToolResultName", boolOf()),
		opt("requiresAssistantAfterToolResult", boolOf()),
		opt("requiresThinkingAsText", boolOf()),
		opt("requiresReasoningContentOnAssistantMessages", boolOf()),
		opt("thinkingFormat", literals("openai", "openrouter", "together", "baseten", "deepseek", "zai", "qwen", "chat-template", "qwen-chat-template", "string-thinking", "ant-ling")),
		opt("chatTemplateKwargs", p2runtime.Record(chatTemplateKwargSchema)),
		opt("chatTemplateArgs", p2runtime.Record(chatTemplateKwargSchema)),
		opt("cacheControlFormat", p2runtime.Literal("anthropic")),
		opt("openRouterRouting", openRouterRoutingSchema),
		opt("vercelGatewayRouting", vercelGatewayRoutingSchema),
		opt("supportsOpenAIGrammarTools", boolOf()),
		opt("supportsStrictMode", boolOf()),
		opt("sendSessionAffinityHeaders", boolOf()),
		opt("sessionAffinityFormat", sessionAffinityFormatSchema),
		opt("supportsLongCacheRetention", boolOf()),
		opt("vllmPriority", num()),
	})

	openAIResponsesCompatSchema = obj(schema.Properties{
		opt("supportsDeveloperRole", boolOf()),
		opt("sessionAffinityFormat", sessionAffinityFormatSchema),
		opt("supportsLongCacheRetention", boolOf()),
		opt("supportsStrictMode", boolOf()),
		opt("supportsOpenAIGrammarTools", boolOf()),
		opt("supportsMaxOutputTokens", boolOf()),
	})

	modelCostRates = func() schema.Properties {
		return schema.Properties{prop("input", num()), prop("output", num()), prop("cacheRead", num()), prop("cacheWrite", num())}
	}
	modelCostTierSchema = obj(append(schema.Properties{prop("inputTokensAbove", num())}, modelCostRates()...))
	modelCostSchema     = obj(append(modelCostRates(), opt("tiers", arr(modelCostTierSchema))))
	modelPromptCache    = obj(schema.Properties{opt("short", num(schema.ExclusiveMinimum(0))), opt("long", num(schema.ExclusiveMinimum(0)))})
	imageResizeSchema   = obj(schema.Properties{
		opt("maxWidth", integer(schema.Minimum(1))),
		opt("maxHeight", integer(schema.Minimum(1))),
		opt("maxBytes", integer(schema.Minimum(1))),
		opt("jpegQuality", integer(schema.Minimum(1), schema.Maximum(100))),
	})
	modelInputLimitsSchema = obj(schema.Properties{
		opt("maxRequestBytes", integer(schema.Minimum(1))),
		opt("images", obj(schema.Properties{
			opt("resize", imageResizeSchema),
			opt("maxPerMessage", integer(schema.Minimum(1))),
			opt("maxPerRequest", integer(schema.Minimum(1))),
		})),
	})

	anthropicMessagesCompatSchema = obj(schema.Properties{
		opt("supportsEagerToolInputStreaming", boolOf()),
		opt("supportsLongCacheRetention", boolOf()),
		opt("sendSessionAffinityHeaders", boolOf()),
		opt("supportsCacheControlOnTools", boolOf()),
		opt("supportsTemperature", boolOf()),
		opt("forceAdaptiveThinking", boolOf()),
		opt("allowEmptySignature", boolOf()),
		opt("supportsStrictTools", boolOf()),
		opt("supportsMidConvoEffort", boolOf()),
		opt("allowedFallbackModels", arr3(obj(schema.Properties{
			prop("provider", nonEmpty()), prop("model", nonEmpty()), prop("cost", modelCostSchema),
		}))),
	})

	providerCompatSchema = union(openAICompletionsCompatSchema, openAIResponsesCompatSchema, anthropicMessagesCompatSchema)

	samplingParamsSchema = p2runtime.Record(schema.Unknown())
	headersSchema        = p2runtime.Record(str())
	inputSchema          = arr(literals("text", "image"))
)

// arr3 is an array of at most three items (`arr(item, { maxItems: 3 })`).
func arr3(item schema.Schema) schema.Schema { return schema.Arr(item, schema.MaxItems(3)) }

var modelDefinitionSchema = obj(schema.Properties{
	prop("id", nonEmpty()),
	opt("name", nonEmpty()),
	opt("api", nonEmpty()),
	opt("baseUrl", nonEmpty()),
	opt("reasoning", boolOf()),
	opt("thinkingLevelMap", thinkingLevelMapSchema),
	opt("input", inputSchema),
	opt("inputLimits", modelInputLimitsSchema),
	opt("cost", modelCostSchema),
	opt("promptCache", modelPromptCache),
	opt("contextWindow", num()),
	opt("maxTokens", num()),
	opt("samplingParams", samplingParamsSchema),
	opt("headers", headersSchema),
	opt("compat", providerCompatSchema),
})

var modelOverrideSchema = obj(schema.Properties{
	opt("name", nonEmpty()),
	opt("reasoning", boolOf()),
	opt("thinkingLevelMap", thinkingLevelMapSchema),
	opt("input", inputSchema),
	opt("inputLimits", modelInputLimitsSchema),
	opt("cost", obj(schema.Properties{
		opt("input", num()),
		opt("output", num()),
		opt("cacheRead", num()),
		opt("cacheWrite", num()),
		opt("tiers", arr(modelCostTierSchema)),
	})),
	opt("promptCache", modelPromptCache),
	opt("contextWindow", num()),
	opt("maxTokens", num()),
	opt("samplingParams", samplingParamsSchema),
	opt("headers", headersSchema),
	opt("compat", providerCompatSchema),
})

var providerConfigSchema = obj(schema.Properties{
	opt("name", nonEmpty()),
	opt("baseUrl", nonEmpty()),
	opt("apiKey", nonEmpty()),
	opt("api", nonEmpty()),
	opt("oauth", p2runtime.Literal("radius")),
	opt("headers", headersSchema),
	opt("compat", providerCompatSchema),
	opt("authHeader", boolOf()),
	opt("models", arr(modelDefinitionSchema)),
	opt("modelOverrides", p2runtime.Record(modelOverrideSchema)),
})

// ModelsConfigSchema is the schema of models.json.
var ModelsConfigSchema = obj(schema.Properties{prop("providers", p2runtime.Record(providerConfigSchema))})

var validateModelsConfig = func() *schema.Validator {
	validator, err := schema.Compile(ModelsConfigSchema, schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return validator
}()

// formatValidationPath is `providers.openai.models.0.id` for the JSON Pointer `/providers/openai/models/0/id` (a missing
// field's own path).
func formatValidationPath(path string) string {
	if path == "" {
		return "root"
	}
	segments := strings.Split(path[1:], "/")
	for i, segment := range segments {
		segments[i] = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
	}
	return strings.Join(segments, ".")
}

// ProviderConfig is a provider of models.json as the file wrote it (immutable: never modify it).
type ProviderConfig struct {
	ID     string
	Config *chordjson.Object
}

// ModelConfig is one immutable load of models.json.
type ModelConfig struct {
	providers []ProviderConfig
	err       string
}

// LoadModelConfig loads models.json at modelsJSONPath: a missing file is an empty configuration, an unreadable or invalid
// one an empty configuration with an error.
func LoadModelConfig(modelsJSONPath string) *ModelConfig {
	path := p2runtime.NormalizePath(modelsJSONPath)
	content, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &ModelConfig{}
		}
		return &ModelConfig{err: fmt.Sprintf("Failed to load models.json: %s\n\nFile: %s", err, path)}
	}
	parsed, err := chordjson.Decode([]byte(p2runtime.StripJSONComments(p2runtime.StripBOM(string(content)))))
	if err != nil {
		return &ModelConfig{err: "Invalid models.json syntax. File: " + path}
	}
	if ok, _ := validateModelsConfig.Check(parsed); !ok {
		errs, _ := validateModelsConfig.Errors(parsed)
		var lines []string
		for _, each := range errs {
			lines = append(lines, fmt.Sprintf("  - %s: %s", formatValidationPath(each.Path), each.Message))
		}
		detail := strings.Join(lines, "\n")
		if detail == "" {
			detail = "Unknown schema error"
		}
		return &ModelConfig{err: fmt.Sprintf("Invalid models.json schema:\n%s\n\nFile: %s", detail, path)}
	}
	providers, _ := parsed.(*chordjson.Object).Value("providers").(*chordjson.Object)
	config := &ModelConfig{}
	for id, provider := range providers.All() {
		copied, _ := chordjson.Copy(provider)
		config.providers = append(config.providers, ProviderConfig{ID: id, Config: copied.(*chordjson.Object)})
	}
	return config
}

// Provider is the provider with id, nil when models.json has none.
func (config *ModelConfig) Provider(id string) *chordjson.Object {
	for _, provider := range config.providers {
		if provider.ID == id {
			return provider.Config
		}
	}
	return nil
}

// ProviderIDs are the providers' ids in the file's order.
func (config *ModelConfig) ProviderIDs() []string {
	ids := make([]string, len(config.providers))
	for i, provider := range config.providers {
		ids[i] = provider.ID
	}
	return ids
}

// Error is why models.json could not be loaded; empty when it could.
func (config *ModelConfig) Error() string { return config.err }
