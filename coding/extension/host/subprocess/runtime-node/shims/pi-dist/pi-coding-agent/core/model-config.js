/** Immutable, credential-blind models.json snapshot. */
import { readFile } from "node:fs/promises";
import { ProviderCompatSchema } from "../../pi-ai/providers/compat-schema.js";
import { ModelCostSchema, ModelInputLimitsSchema, ModelInputModalitySchema, ModelPromptCacheSchema, ThinkingLevelMapSchema, } from "../../pi-ai/providers/model-schema.js";
import { Type } from "../../../typebox.mjs";
import { Compile } from "../../../typebox-compile.mjs";
import { stripJsonComments } from "../utils/json.js";
import { normalizePath } from "../utils/paths.js";
import { stripBom } from "../utils/text.js";
const SamplingParamsSchema = Type.Record(Type.String(), Type.Unknown());
const SamplingParamsByThinkingLevelSchema = Type.Object({
    off: Type.Optional(SamplingParamsSchema),
    minimal: Type.Optional(SamplingParamsSchema),
    low: Type.Optional(SamplingParamsSchema),
    medium: Type.Optional(SamplingParamsSchema),
    high: Type.Optional(SamplingParamsSchema),
    xhigh: Type.Optional(SamplingParamsSchema),
    max: Type.Optional(SamplingParamsSchema),
});
const PositiveTokenCountSchema = Type.Number({ exclusiveMinimum: 0 });
const ModelDefinitionSchema = Type.Object({
    id: Type.String({ minLength: 1 }),
    name: Type.Optional(Type.String({ minLength: 1 })),
    api: Type.Optional(Type.String({ minLength: 1 })),
    baseUrl: Type.Optional(Type.String({ minLength: 1 })),
    reasoning: Type.Optional(Type.Boolean()),
    thinkingLevelMap: Type.Optional(ThinkingLevelMapSchema),
    input: Type.Optional(Type.Array(ModelInputModalitySchema)),
    inputLimits: Type.Optional(ModelInputLimitsSchema),
    cost: Type.Optional(ModelCostSchema),
    promptCache: Type.Optional(ModelPromptCacheSchema),
    contextWindow: Type.Optional(PositiveTokenCountSchema),
    maxTokens: Type.Optional(PositiveTokenCountSchema),
    samplingParams: Type.Optional(SamplingParamsSchema),
    samplingParamsByThinkingLevel: Type.Optional(SamplingParamsByThinkingLevelSchema),
    headers: Type.Optional(Type.Record(Type.String(), Type.String())),
    compat: Type.Optional(ProviderCompatSchema),
});
const ModelOverrideSchema = Type.Object({
    name: Type.Optional(Type.String({ minLength: 1 })),
    reasoning: Type.Optional(Type.Boolean()),
    thinkingLevelMap: Type.Optional(ThinkingLevelMapSchema),
    input: Type.Optional(Type.Array(ModelInputModalitySchema)),
    inputLimits: Type.Optional(ModelInputLimitsSchema),
    cost: Type.Optional(Type.Partial(ModelCostSchema)),
    promptCache: Type.Optional(ModelPromptCacheSchema),
    contextWindow: Type.Optional(PositiveTokenCountSchema),
    maxTokens: Type.Optional(PositiveTokenCountSchema),
    samplingParams: Type.Optional(SamplingParamsSchema),
    samplingParamsByThinkingLevel: Type.Optional(SamplingParamsByThinkingLevelSchema),
    headers: Type.Optional(Type.Record(Type.String(), Type.String())),
    compat: Type.Optional(ProviderCompatSchema),
});
const ProviderConfigSchema = Type.Object({
    name: Type.Optional(Type.String({ minLength: 1 })),
    baseUrl: Type.Optional(Type.String({ minLength: 1 })),
    apiKey: Type.Optional(Type.String({ minLength: 1 })),
    api: Type.Optional(Type.String({ minLength: 1 })),
    oauth: Type.Optional(Type.Literal("radius")),
    headers: Type.Optional(Type.Record(Type.String(), Type.String())),
    compat: Type.Optional(ProviderCompatSchema),
    authHeader: Type.Optional(Type.Boolean()),
    models: Type.Optional(Type.Array(ModelDefinitionSchema)),
    modelOverrides: Type.Optional(Type.Record(Type.String(), ModelOverrideSchema)),
});
const ModelsConfigProperties = {
    providers: Type.Record(Type.String(), ProviderConfigSchema),
};
export const ModelsConfigSchema = Type.Object({
    $schema: Type.Optional(Type.String()),
    ...ModelsConfigProperties,
});
const validateModelsConfig = Compile(ModelsConfigSchema);
function formatValidationPath(error) {
    if (error.keyword === "required") {
        const requiredProperties = error.params.requiredProperties;
        const requiredProperty = requiredProperties?.[0];
        if (requiredProperty) {
            const basePath = error.instancePath.replace(/^\//, "").replace(/\//g, ".");
            return basePath ? `${basePath}.${requiredProperty}` : requiredProperty;
        }
    }
    const path = error.instancePath.replace(/^\//, "").replace(/\//g, ".");
    return path || "root";
}
function deepFreeze(value) {
    if (typeof value !== "object" || value === null || Object.isFrozen(value))
        return value;
    for (const child of Object.values(value))
        deepFreeze(child);
    return Object.freeze(value);
}
/** One immutable load of models.json. */
export class ModelConfig {
    providers;
    error;
    constructor(providers, error) {
        this.providers = providers;
        this.error = error;
    }
    static async load(modelsJsonPath) {
        if (!modelsJsonPath)
            return new ModelConfig(new Map());
        const path = normalizePath(modelsJsonPath);
        let content;
        try {
            content = await readFile(path, "utf-8");
        }
        catch (error) {
            if (error.code === "ENOENT")
                return new ModelConfig(new Map());
            return new ModelConfig(new Map(), `Failed to load models.json: ${error instanceof Error ? error.message : error}\n\nFile: ${path}`);
        }
        let parsed;
        try {
            parsed = JSON.parse(stripJsonComments(stripBom(content)));
        }
        catch (error) {
            return new ModelConfig(new Map(), `Failed to parse models.json: ${error instanceof Error ? error.message : error}\n\nFile: ${path}`);
        }
        if (!validateModelsConfig.Check(parsed)) {
            const errors = validateModelsConfig
                .Errors(parsed)
                .map((error) => `  - ${formatValidationPath(error)}: ${error.message}`)
                .join("\n") || "Unknown schema error";
            return new ModelConfig(new Map(), `Invalid models.json schema:\n${errors}\n\nFile: ${path}`);
        }
        const providers = new Map();
        for (const [providerId, provider] of Object.entries(parsed.providers)) {
            providers.set(providerId, deepFreeze(structuredClone(provider)));
        }
        return new ModelConfig(providers);
    }
    getProvider(providerId) {
        return this.providers.get(providerId);
    }
    getProviderIds() {
        return [...this.providers.keys()];
    }
    getError() {
        return this.error;
    }
}
//# sourceMappingURL=model-config.js.map