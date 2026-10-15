package configschema

// Mirrors packages/coding-agent/src/core/model-config.ts.

func samplingParams() *Schema { return Record(Unknown()) }

func samplingParamsByThinkingLevel() *Schema {
	var props []Prop
	for _, level := range modelThinkingLevels {
		props = append(props, Optional(level, samplingParams()))
	}
	return Object(props)
}

func positiveTokenCount() *Schema { return Number(Opt{"exclusiveMinimum", 0}) }

func nonEmptyString() *Schema { return String(Opt{"minLength", 1}) }

// modelSchemaParts holds the model-config.ts module constants that several schemas reference. Pi builds each once and the
// schemas share it; ModelsConfigSchema does the same, so the tree holds one ProviderCompatSchema (about 750 objects) instead of
// three. Validation and Render only read the tree.
type modelSchemaParts struct {
	compat, thinkingLevelMap, inputLimits, cost, promptCache, samplingParams, samplingParamsByThinkingLevel *Schema
}

func newModelSchemaParts() modelSchemaParts {
	return modelSchemaParts{
		compat:                        ProviderCompatSchema(),
		thinkingLevelMap:              ThinkingLevelMapSchema(),
		inputLimits:                   ModelInputLimitsSchema(),
		cost:                          ModelCostSchema(),
		promptCache:                   ModelPromptCacheSchema(),
		samplingParams:                samplingParams(),
		samplingParamsByThinkingLevel: samplingParamsByThinkingLevel(),
	}
}

func modelDefinitionSchema(parts modelSchemaParts) *Schema {
	return Object([]Prop{
		P("id", nonEmptyString()),
		Optional("name", nonEmptyString()),
		Optional("api", nonEmptyString()),
		Optional("baseUrl", nonEmptyString()),
		Optional("reasoning", Boolean()),
		Optional("thinkingLevelMap", parts.thinkingLevelMap),
		Optional("input", Array(ModelInputModalitySchema())),
		Optional("inputLimits", parts.inputLimits),
		Optional("cost", parts.cost),
		Optional("promptCache", parts.promptCache),
		Optional("contextWindow", positiveTokenCount()),
		Optional("maxTokens", positiveTokenCount()),
		Optional("samplingParams", parts.samplingParams),
		Optional("samplingParamsByThinkingLevel", parts.samplingParamsByThinkingLevel),
		Optional("headers", Record(String())),
		Optional("compat", parts.compat),
	})
}

func modelOverrideSchema(parts modelSchemaParts) *Schema {
	return Object([]Prop{
		Optional("name", nonEmptyString()),
		Optional("reasoning", Boolean()),
		Optional("thinkingLevelMap", parts.thinkingLevelMap),
		Optional("input", Array(ModelInputModalitySchema())),
		Optional("inputLimits", parts.inputLimits),
		Optional("cost", Partial(parts.cost)),
		Optional("promptCache", parts.promptCache),
		Optional("contextWindow", positiveTokenCount()),
		Optional("maxTokens", positiveTokenCount()),
		Optional("samplingParams", parts.samplingParams),
		Optional("samplingParamsByThinkingLevel", parts.samplingParamsByThinkingLevel),
		Optional("headers", Record(String())),
		Optional("compat", parts.compat),
	})
}

func providerConfigSchema(parts modelSchemaParts) *Schema {
	return Object([]Prop{
		Optional("name", nonEmptyString()),
		Optional("baseUrl", nonEmptyString()),
		Optional("apiKey", nonEmptyString()),
		Optional("api", nonEmptyString()),
		Optional("oauth", Literal("radius")),
		Optional("headers", Record(String())),
		Optional("compat", parts.compat),
		Optional("authHeader", Boolean()),
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
