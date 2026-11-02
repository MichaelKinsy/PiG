package configschema

// Mirrors packages/coding-agent/src/core/pi-ai-schemas.ts. Pi wraps pi-ai's pi-schema-built configuration schemas as Type.Unsafe so the
// TypeBox-built models.json and settings.json schemas can use them: the same JSON, typed for TypeBox's Static. A Schema of this package is
// that JSON, so ProviderCompatSchema, ModelCostSchema, ModelInputLimitsSchema, ModelInputModalitySchema, ModelPromptCacheSchema,
// ModelThinkingLevelSchema and ThinkingLevelMapSchema are the trees of compat_a.go, compat_b.go and model.go as they are; only the schema
// that is not a pi-ai export is defined here.

// PartialModelCostSchema is ModelCostSchema with every field optional, for model overrides that change some of the rates: the cost schema
// without its own required list (the tiers keep theirs).
func PartialModelCostSchema() *Schema {
	cost := ModelCostSchema()
	kept := cost.kv[:0]
	for _, o := range cost.kv {
		if o.K != "required" {
			kept = append(kept, o)
		}
	}
	cost.kv = kept
	return cost
}
