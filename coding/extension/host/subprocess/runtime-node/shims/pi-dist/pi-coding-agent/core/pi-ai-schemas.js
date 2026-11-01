/**
 * pi-ai's configuration schemas (built with pi-schema) as TypeBox schemas, for the coding agent's own TypeBox-built
 * configuration schemas (models.json, settings.json): the same JSON, typed for TypeBox's `Static`.
 */
import { ProviderCompatSchema as ProviderCompatJsonSchema, } from "../../pi-ai/providers/compat-schema.js";
import { ModelCostSchema as ModelCostJsonSchema, ModelInputLimitsSchema as ModelInputLimitsJsonSchema, ModelInputModalitySchema as ModelInputModalityJsonSchema, ModelPromptCacheSchema as ModelPromptCacheJsonSchema, ModelThinkingLevelSchema as ModelThinkingLevelJsonSchema, ThinkingLevelMapSchema as ThinkingLevelMapJsonSchema, } from "../../pi-ai/providers/model-schema.js";
import { Type } from "../../../typebox.mjs";
export const ProviderCompatSchema = Type.Unsafe(ProviderCompatJsonSchema);
export const ModelCostSchema = Type.Unsafe(ModelCostJsonSchema);
export const ModelInputLimitsSchema = Type.Unsafe(ModelInputLimitsJsonSchema);
export const ModelInputModalitySchema = Type.Unsafe(ModelInputModalityJsonSchema);
export const ModelPromptCacheSchema = Type.Unsafe(ModelPromptCacheJsonSchema);
export const ModelThinkingLevelSchema = Type.Unsafe(ModelThinkingLevelJsonSchema);
export const ThinkingLevelMapSchema = Type.Unsafe(ThinkingLevelMapJsonSchema);
/** `ModelCostSchema` with every field optional, for model overrides that change some of the rates. */
export const PartialModelCostSchema = Type.Unsafe(withoutRequired(ModelCostJsonSchema));
function withoutRequired(schema) {
    const { required: _required, ...rest } = schema;
    return rest;
}
//# sourceMappingURL=pi-ai-schemas.js.map