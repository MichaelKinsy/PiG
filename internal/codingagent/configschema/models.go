package configschema

// Mirrors packages/coding-agent/src/core/model-config.ts.

func samplingParams() *Schema { return Record(Unknown()) }

func samplingParamsByThinkingLevel() *Schema {
	var props []Prop
	for _, level := range modelThinkingLevels {
		props = append(props, Optional(level, samplingParams()))
	}
	return Object(props, D("samplingParams per thinking level, merged over samplingParams."))
}

func samplingParamsField() *Schema {
	return Record(Unknown(), D("Fields merged into every request body. Applies only to OpenAI-compatible APIs."))
}

func headersSchema() *Schema {
	return Record(String(), D("Request headers. Values resolve like apiKey."))
}

// Model fields shared by models entries and modelOverrides entries (ModelFieldDescriptions); defaults apply only to models entries.
const (
	modelNameDescription          = "Display name, also matched by --model patterns."
	modelReasoningDescription     = "Whether the model supports thinking."
	modelContextWindowDescription = "Context window in tokens."
	modelMaxTokensDescription     = "Maximum output tokens."
)

func nonEmptyString(opts ...Opt) *Schema { return String(append([]Opt{{"minLength", 1}}, opts...)...) }

// modelSchemaParts holds the model-config.ts module constants that several schemas reference. Pi builds each once and the
// schemas share it; ModelsConfigSchema does the same, so the tree holds one ProviderCompatSchema (about 750 objects) instead of
// three. Validation and Render only read the tree.
type modelSchemaParts struct {
	compat, thinkingLevelMap, inputLimits, cost, promptCache, samplingParams, samplingParamsByThinkingLevel, headers *Schema
}

func newModelSchemaParts() modelSchemaParts {
	return modelSchemaParts{
		compat:                        ProviderCompatSchema(),
		thinkingLevelMap:              ThinkingLevelMapSchema(),
		inputLimits:                   ModelInputLimitsSchema(),
		cost:                          ModelCostSchema(),
		promptCache:                   ModelPromptCacheSchema(),
		samplingParams:                samplingParamsField(),
		samplingParamsByThinkingLevel: samplingParamsByThinkingLevel(),
		headers:                       headersSchema(),
	}
}

func modelDefinitionSchema(parts modelSchemaParts) *Schema {
	return Object([]Prop{
		P("id", nonEmptyString(D("Model ID sent to the API."))),
		Optional("name", nonEmptyString(D(modelNameDescription))),
		Optional("api", nonEmptyString(D("Overrides the provider api."))),
		Optional("baseUrl", nonEmptyString(D("Overrides the provider baseUrl."))),
		Optional("reasoning", Boolean(Default(false), D(modelReasoningDescription))),
		Optional("thinkingLevelMap", parts.thinkingLevelMap),
		Optional("input", Array(ModelInputModalitySchema(), Default([]any{"text"}))),
		Optional("inputLimits", parts.inputLimits),
		Optional("cost", parts.cost),
		Optional("promptCache", parts.promptCache),
		Optional("contextWindow", Number(Opt{"exclusiveMinimum", 0}, Default(128000), D(modelContextWindowDescription))),
		Optional("maxTokens", Number(Opt{"exclusiveMinimum", 0}, Default(16384), D(modelMaxTokensDescription))),
		Optional("samplingParams", parts.samplingParams),
		Optional("samplingParamsByThinkingLevel", parts.samplingParamsByThinkingLevel),
		Optional("headers", parts.headers),
		Optional("compat", parts.compat),
	})
}

func modelOverrideSchema(parts modelSchemaParts) *Schema {
	return Object([]Prop{
		Optional("name", nonEmptyString(D(modelNameDescription))),
		Optional("reasoning", Boolean(D(modelReasoningDescription))),
		Optional("thinkingLevelMap", parts.thinkingLevelMap),
		Optional("input", Array(ModelInputModalitySchema())),
		Optional("inputLimits", parts.inputLimits),
		Optional("cost", Partial(parts.cost)),
		Optional("promptCache", parts.promptCache),
		Optional("contextWindow", Number(Opt{"exclusiveMinimum", 0}, D(modelContextWindowDescription))),
		Optional("maxTokens", Number(Opt{"exclusiveMinimum", 0}, D(modelMaxTokensDescription))),
		Optional("samplingParams", parts.samplingParams),
		Optional("samplingParamsByThinkingLevel", parts.samplingParamsByThinkingLevel),
		Optional("headers", parts.headers),
		Optional("compat", parts.compat),
	})
}

func providerConfigSchema(parts modelSchemaParts) *Schema {
	return Object([]Prop{
		Optional("name", nonEmptyString(D("Display name."))),
		Optional("baseUrl", nonEmptyString(D("API base URL. Also applies to the provider's built-in models."))),
		Optional("apiKey", nonEmptyString(D(`Literal key, "$NAME" environment interpolation, or "!command" output.`))),
		Optional("api", nonEmptyString(D(`API for the provider's models, such as "openai-completions", "openai-responses", "anthropic-messages", or "google-generative-ai".`))),
		Optional("oauth", Literal("radius", D("OAuth login flow. Requires baseUrl."))),
		Optional("headers", parts.headers),
		Optional("compat", parts.compat),
		Optional("authHeader", Boolean(Default(false), D("Whether to send the resolved API key as Authorization: Bearer."))),
		Optional("models", Array(modelDefinitionSchema(parts))),
		Optional("modelOverrides", Record(modelOverrideSchema(parts))),
	})
}

func ModelsConfigSchema() *Schema {
	return Object([]Prop{Optional("$schema", String()), P("providers", Record(providerConfigSchema(newModelSchemaParts())))})
}

func modelsArtifact() Artifact {
	return Artifact{
		Path:   "schemas/models.schema.json",
		Schema: ModelsConfigSchema(),
		Definitions: []Definition{
			{"ModelCost", ModelCostSchema()},
			{"ModelInputLimits", ModelInputLimitsSchema()},
			{"ModelPromptCache", ModelPromptCacheSchema()},
			{"ProviderCompat", ProviderCompatSchema()},
			{"ThinkingLevelMap", ThinkingLevelMapSchema()},
		},
	}
}

// KeybindingsArtifactPath is the path of the keybindings artifact, which the caller supplies to Render and ArtifactPaths.
const KeybindingsArtifactPath = "schemas/keybindings.schema.json"

func artifacts(keybindings Artifact) []Artifact {
	return []Artifact{modelsArtifact(), settingsArtifact(), keybindings, themeArtifact()}
}

// ArtifactPaths lists the artifact paths in generation order (generate-schemas.ts schemaArtifacts).
func ArtifactPaths() []string {
	var paths []string
	for _, a := range artifacts(Artifact{Path: KeybindingsArtifactPath}) {
		paths = append(paths, a.Path)
	}
	return paths
}

// Render returns the rendered artifacts by path; keybindings is the keybindings artifact (codingagent.KeybindingsSchemaArtifact).
func Render(keybindings Artifact) (map[string]string, error) {
	out := map[string]string{}
	for _, a := range artifacts(keybindings) {
		text, err := a.Render()
		if err != nil {
			return nil, err
		}
		out[a.Path] = text
	}
	return out, nil
}
