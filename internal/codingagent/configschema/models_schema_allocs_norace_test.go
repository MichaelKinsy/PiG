//go:build !race

package configschema

import "testing"

// modelsConfigSchemaObjectsCeiling bounds one build of the models.json schema tree. model-config.ts declares ProviderCompatSchema,
// ModelCostSchema, ThinkingLevelMapSchema and the other shared schemas once and references them from the provider, model and
// override schemas; the tree shares them the same way. It measured 1,108 objects; with a copy per reference, 2,832. Pi v1.4.0's SystemOneCompatSchema (imageInput, sendImageOptInHeader)
// merges into the provider compat union and takes it to 1,166.
const modelsConfigSchemaObjectsCeiling = 1_200

func TestModelsConfigSchemaSharesPiConstants(t *testing.T) {
	objects := testing.AllocsPerRun(10, func() { ModelsConfigSchema() })
	t.Logf("ModelsConfigSchema allocates %.0f objects", objects)
	if objects > modelsConfigSchemaObjectsCeiling {
		t.Errorf("ModelsConfigSchema allocates %.0f objects, ceiling %d", objects, modelsConfigSchemaObjectsCeiling)
	}
}
