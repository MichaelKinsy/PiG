package configschema

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Compares every export of packages/ai/src/providers/model-schema.ts with its Go counterpart in model.go: the schema constants as the
// JSON text TypeBox builds (JSON.stringify(schema, null, 2)) and the literal lists as the arrays Pi declares.

// piSchemaExports returns JSON.stringify(value, null, 2) of every export of Pi's ai/providers/<module>.js.
func piSchemaExports(t *testing.T, module string) map[string]string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "node", "testdata/ai_schema_module.mjs", pigversion.UpstreamPackageVersion, module)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var exports map[string]string
	if err := json.Unmarshal(output, &exports); err != nil {
		t.Fatal(err)
	}
	return exports
}

func schemaText(s *Schema) string {
	var buf bytes.Buffer
	marshal(s, "", &buf)
	return buf.String()
}

func listText(t *testing.T, items []string) string {
	t.Helper()
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestModelSchemaExportsMatchPi(t *testing.T) {
	pi := piSchemaExports(t, "model-schema")
	for name, build := range map[string]func() *Schema{
		"ThinkingLevelSchema":           ThinkingLevelSchema,
		"ModelThinkingLevelSchema":      ModelThinkingLevelSchema,
		"ModelInputModalitySchema":      ModelInputModalitySchema,
		"CacheRetentionSchema":          CacheRetentionSchema,
		"ThinkingLevelMapSchema":        ThinkingLevelMapSchema,
		"ModelPromptCacheSchema":        ModelPromptCacheSchema,
		"ModelCostRatesSchema":          ModelCostRatesSchema,
		"ModelCostTierSchema":           ModelCostTierSchema,
		"ModelCostSchema":               ModelCostSchema,
		"ModelImageResizeOptionsSchema": ModelImageResizeOptionsSchema,
		"ModelImageInputLimitsSchema":   ModelImageInputLimitsSchema,
		"ModelInputLimitsSchema":        ModelInputLimitsSchema,
	} {
		want, ok := pi[name]
		if !ok {
			t.Errorf("Pi has no export %s", name)
			continue
		}
		if got := schemaText(build()); got != want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, want)
		}
	}
	for name, list := range map[string][]string{
		"THINKING_LEVELS":               thinkingLevels,
		"MODEL_THINKING_LEVELS":         modelThinkingLevels,
		"MODEL_INPUT_MODALITIES":        modelInputModalities,
		"CACHE_RETENTIONS":              cacheRetentions,
		"MODEL_PROMPT_CACHE_RETENTIONS": modelPromptCacheRetentions,
	} {
		if got := listText(t, list); got != pi[name] {
			t.Errorf("%s = %s, want %s", name, got, pi[name])
		}
	}
	var exported []string
	for name := range pi {
		exported = append(exported, name)
	}
	slices.Sort(exported)
	if len(exported) != 17 {
		t.Errorf("Pi model-schema.js exports %v: 17 expected, so a new export needs a Go counterpart", exported)
	}
}
