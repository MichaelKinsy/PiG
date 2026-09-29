package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtureGeneratedAt = "2026-07-23T10:00:00.000Z"

type modelDataFixture struct {
	root, dir string
	structure ModelDataStructure
	values    map[string]any
}

func newModelDataFixture(t testing.TB) modelDataFixture {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "src", "providers", "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeModelFixture(t, filepath.Join(root, "src", "models.generated.ts"), `import { TEST_PROVIDER_MODELS } from "./providers/test-provider.models.ts";`+"\n")
	writeModelFixture(t, filepath.Join(root, "src", "providers", "test-provider.models.ts"), "import values from \"./data/test-provider.json\" with { type: \"json\" };\nimport { flattenModelCatalog, type ModelCatalog } from \"../model-catalog.ts\";\n\nexport const TEST_PROVIDER_MODELS: ModelCatalog<typeof values, \"test-provider\"> =\n\tflattenModelCatalog(\"test-provider\", values);\n")
	f := modelDataFixture{root, dir, ModelDataStructure{"test-provider": {"model-a": "openai-completions"}}, map[string]any{"model-a": map[string]any{"id": "model-a", "name": "Model A", "api": "openai-completions", "provider": "test-provider", "baseUrl": "https://example.test/v1", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}}}
	f.write(t, ModelDataSchemaVersion, "openai-completions")
	return f
}

func writeModelFixture(t testing.TB, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func modelFixtureJSON(t testing.TB, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

func (f modelDataFixture) write(t testing.TB, schema int, api string) {
	t.Helper()
	content := modelFixtureJSON(t, map[string]any{api: f.values})
	writeModelFixture(t, filepath.Join(f.dir, "test-provider.json"), content)
	manifest := CreateModelDataManifest(f.structure, map[string]string{"test-provider.json": content}, fixtureGeneratedAt)
	manifest.SchemaVersion = schema
	writeModelFixture(t, filepath.Join(f.dir, ModelDataManifestFile), modelFixtureJSON(t, manifest))
}

func requireModelDataError(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v; want %q", err, want)
	}
}

func TestModelDataValidationUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:81
	t.Run("rejects a missing upstream model from an exact generated allowlist", func(t *testing.T) {
		requireModelDataError(t, AssertExactModelIds("qwen-token-plan-individual", []string{"model-a", "model-b"}, []string{"model-a"}), "qwen-token-plan-individual model IDs do not match (missing: model-b)")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:87
	t.Run("rejects an unexpected model from an exact generated allowlist", func(t *testing.T) {
		requireModelDataError(t, AssertExactModelIds("test-provider", []string{"model-a"}, []string{"model-a", "model-b"}), "test-provider model IDs do not match (extra: model-b)")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:93
	t.Run("reads and validates API-grouped model data", func(t *testing.T) {
		f := newModelDataFixture(t)
		got, err := ReadModelDataStructure(f.root)
		if err != nil || !reflect.DeepEqual(got, f.structure) {
			t.Fatalf("structure=%v, err=%v", got, err)
		}
		if err := ValidateModelDataDirectory(f.structure, f.dir); err != nil {
			t.Fatal(err)
		}
		if err := ValidateGeneratedModelData(f.root); err != nil {
			t.Fatal(err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:99
	t.Run("rejects a missing model data directory", func(t *testing.T) {
		f := newModelDataFixture(t)
		if err := os.RemoveAll(f.dir); err != nil {
			t.Fatal(err)
		}
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "does not exist")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:105
	for _, row := range []struct{ field, value string }{{"id", "wrong-id"}, {"provider", "wrong-provider"}, {"api", "anthropic-messages"}} {
		t.Run("rejects a wrong model "+row.field, func(t *testing.T) {
			f := newModelDataFixture(t)
			f.values["model-a"].(map[string]any)[row.field] = row.value
			f.write(t, ModelDataSchemaVersion, "openai-completions")
			requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "has "+row.field)
		})
	}
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:117
	t.Run("rejects a model in the wrong API group", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.write(t, ModelDataSchemaVersion, "anthropic-messages")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "grouped under API")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:129
	t.Run("rejects duplicate model IDs across API groups", func(t *testing.T) {
		f := newModelDataFixture(t)
		content := `{"openai-completions":` + strings.TrimSpace(modelFixtureJSON(t, f.values)) + `,"anthropic-messages":` + strings.TrimSpace(modelFixtureJSON(t, f.values)) + "}\n"
		writeModelFixture(t, filepath.Join(f.dir, "test-provider.json"), content)
		manifest := CreateModelDataManifest(f.structure, map[string]string{"test-provider.json": content}, fixtureGeneratedAt)
		writeModelFixture(t, filepath.Join(f.dir, ModelDataManifestFile), modelFixtureJSON(t, manifest))
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "more than one API group")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:142
	t.Run("rejects missing model IDs and stale file hashes", func(t *testing.T) {
		f := newModelDataFixture(t)
		writeModelFixture(t, filepath.Join(f.dir, "test-provider.json"), "{}\n")
		err := ValidateModelDataDirectory(f.structure, f.dir)
		if err == nil || !(strings.Contains(err.Error(), "manifest hash") || strings.Contains(err.Error(), "model IDs")) {
			t.Fatalf("error = %v", err)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:148
	t.Run("rejects incompatible schema and generation stamps", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.write(t, ModelDataSchemaVersion+1, "openai-completions")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "model data schema")
		f.mutateManifest(t, "structureHash", "stale")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "generation stamp")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:160
	t.Run("rejects an invalid generation timestamp", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.mutateManifest(t, "generatedAt", "invalid")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "generation timestamp")
	})
	// .upstream/v0.87.1/packages/ai/test/model-data-validation.test.ts:169
	t.Run("rejects missing provider shards imported by the aggregator", func(t *testing.T) {
		f := newModelDataFixture(t)
		writeModelFixture(t, filepath.Join(f.root, "src", "models.generated.ts"), "import { TEST_PROVIDER_MODELS } from \"./providers/test-provider.models.ts\";\nimport { MISSING_MODELS } from \"./providers/missing.models.ts\";\n")
		_, err := ReadModelDataStructure(f.root)
		requireModelDataError(t, err, "aggregator and provider shards do not match")
	})
}

func (f modelDataFixture) mutateManifest(t *testing.T, key, value string) {
	t.Helper()
	path := filepath.Join(f.dir, ModelDataManifestFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest[key] = value
	writeModelFixture(t, path, modelFixtureJSON(t, manifest))
}
