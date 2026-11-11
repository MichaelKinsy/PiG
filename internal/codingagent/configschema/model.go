package configschema

// Mirrors packages/ai/src/providers/model-schema.ts.

// The lists are literals, so package initialization allocates nothing for them; modelThinkingLevels is ["off", ...THINKING_LEVELS].
var (
	thinkingLevels       = []string{"minimal", "low", "medium", "high", "xhigh", "max"}
	modelThinkingLevels  = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	modelInputModalities = []string{"text", "image"}

	cacheRetentions            = []string{"none", "short", "long"}
	modelPromptCacheRetentions = []string{"short", "long"}
)

func ThinkingLevelSchema() *Schema      { return OneOf(thinkingLevels) }
func ModelThinkingLevelSchema() *Schema { return OneOf(modelThinkingLevels) }
func ModelInputModalitySchema() *Schema { return OneOf(modelInputModalities) }
func CacheRetentionSchema() *Schema     { return OneOf(cacheRetentions) }

// ThinkingLevelMapSchema lists the seven levels as optional properties of one openObject, each a string or null.
func ThinkingLevelMapSchema() *Schema {
	value := Union([]*Schema{String(), Null()})
	props := make([]Prop, len(modelThinkingLevels))
	for i, level := range modelThinkingLevels {
		props[i] = Optional(level, value)
	}
	return OpenObject(props, D("Provider value sent for each thinking level. null marks a level unsupported. Levels the model does not map use the provider's default value through high; xhigh and max are unsupported."))
}

func ModelPromptCacheSchema() *Schema {
	lifetime := Number(Opt{"exclusiveMinimum", 0})
	return OpenObject([]Prop{Optional("short", lifetime), Optional("long", lifetime)}, D("Best-effort prompt cache lifetime in seconds for the short and long retention tiers. Pi warms only tiers with a known lifetime."))
}

func modelCostRates() []Prop {
	return []Prop{
		P("input", Number(D("Input cost in USD per million tokens."))),
		P("output", Number(D("Output cost in USD per million tokens."))),
		P("cacheRead", Number(D("Cache-read cost in USD per million tokens."))),
		P("cacheWrite", Number(D("Cache-write cost in USD per million tokens."))),
	}
}

func ModelCostRatesSchema() *Schema { return OpenObject(modelCostRates()) }

func ModelCostTierSchema() *Schema {
	props := []Prop{P("inputTokensAbove", Number(D("Use this tier when total request input exceeds this token count.")))}
	return OpenObject(append(props, modelCostRates()...))
}

func ModelCostSchema() *Schema {
	props := modelCostRates()
	return OpenObject(append(props, Optional("tiers", Array(ModelCostTierSchema(), D("Request-wide pricing tiers. The highest matching input threshold applies to the full request.")))))
}

func modelImageResizeOptions(opts ...Opt) *Schema {
	return OpenObject([]Prop{
		Optional("maxWidth", Integer(Opt{"minimum", 1})),
		Optional("maxHeight", Integer(Opt{"minimum", 1})),
		Optional("maxBytes", Integer(Opt{"minimum", 1}, D("Maximum base64-encoded payload size in bytes."))),
		Optional("jpegQuality", Integer(Opt{"minimum", 1}, Opt{"maximum", 100})),
	}, opts...)
}

func ModelImageResizeOptionsSchema() *Schema { return modelImageResizeOptions() }

func ModelImageInputLimitsSchema() *Schema {
	return OpenObject([]Prop{
		Optional("resize", modelImageResizeOptions(D("Cache-safe resize profile applied before a new image enters conversation history."))),
		Optional("maxPerMessage", Integer(Opt{"minimum", 1}, D("Maximum images accepted in one provider message."))),
		Optional("maxPerRequest", Integer(Opt{"minimum", 1}, D("Maximum images accepted across one provider request."))),
	})
}

func ModelInputLimitsSchema() *Schema {
	return OpenObject([]Prop{
		Optional("maxRequestBytes", Integer(Opt{"minimum", 1}, D("Maximum serialized provider request size in bytes."))),
		Optional("images", ModelImageInputLimitsSchema()),
	})
}
