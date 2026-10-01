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
	writeModelFixture(t, filepath.Join(root, "src", "models.generated.ts"), `import { TEST_PROVIDER_CLASSIFIER_MODELS, TEST_PROVIDER_IMAGE_MODELS, TEST_PROVIDER_MODELS } from "./providers/test-provider.models.ts";`+"\n")
	writeModelFixture(t, filepath.Join(root, "src", "providers", "test-provider.models.ts"), "import values from \"./data/test-provider.json\" with { type: \"json\" };\n")
	f := modelDataFixture{root, dir, ModelDataStructure{"test-provider": {"chat:model-a": "openai-completions"}}, map[string]any{"chat:model-a": map[string]any{"type": "chat", "id": "model-a", "name": "Model A", "api": "openai-completions", "provider": "test-provider", "baseUrl": "https://example.test/v1", "reasoning": false, "input": []string{"text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000, "maxTokens": 100}}}
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
	f.writeData(t, f.structure, f.values, schema, api)
}

// writeData mirrors writeFixtureData in the upstream test: one API group, its manifest and the structure hash.
func (f modelDataFixture) writeData(t testing.TB, structure ModelDataStructure, values map[string]any, schema int, api string) {
	t.Helper()
	content := modelFixtureJSON(t, map[string]any{api: values})
	writeModelFixture(t, filepath.Join(f.dir, "test-provider.json"), content)
	manifest := CreateModelDataManifest(structure, map[string]string{"test-provider.json": content}, fixtureGeneratedAt)
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
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:82
	t.Run("rejects a missing upstream model from an exact generated allowlist", func(t *testing.T) {
		requireModelDataError(t, AssertExactModelIds("qwen-token-plan-individual", []string{"model-a", "model-b"}, []string{"model-a"}), "qwen-token-plan-individual model IDs do not match (missing: model-b)")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:88
	t.Run("rejects an unexpected model from an exact generated allowlist", func(t *testing.T) {
		requireModelDataError(t, AssertExactModelIds("test-provider", []string{"model-a"}, []string{"model-a", "model-b"}), "test-provider model IDs do not match (extra: model-b)")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:94
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
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:100
	t.Run("rejects a missing model data directory", func(t *testing.T) {
		f := newModelDataFixture(t)
		if err := os.RemoveAll(f.dir); err != nil {
			t.Fatal(err)
		}
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "does not exist")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:106
	for _, row := range []struct{ field, value string }{{"id", "wrong-id"}, {"provider", "wrong-provider"}, {"api", "anthropic-messages"}} {
		t.Run("rejects a wrong model "+row.field, func(t *testing.T) {
			f := newModelDataFixture(t)
			f.values["chat:model-a"].(map[string]any)[row.field] = row.value
			f.write(t, ModelDataSchemaVersion, "openai-completions")
			requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "has "+row.field)
		})
	}
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:118
	t.Run("rejects a model without a known type", func(t *testing.T) {
		f := newModelDataFixture(t)
		delete(f.values["chat:model-a"].(map[string]any), "type")
		f.write(t, ModelDataSchemaVersion, "openai-completions")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), `expected "chat", "image", or "classifier"`)
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:128
	t.Run("validates image models with output modalities and without chat limits", func(t *testing.T) {
		f := newModelDataFixture(t)
		structure := ModelDataStructure{"test-provider": {"image:image-a": "test-images"}}
		image := map[string]any{"type": "image", "id": "image-a", "name": "Image A", "api": "test-images", "provider": "test-provider", "baseUrl": "https://example.test/v1", "input": []string{"text"}, "output": []string{"image", "text"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}}
		validate := func() error {
			f.writeData(t, structure, map[string]any{"image:image-a": image}, ModelDataSchemaVersion, "test-images")
			return ValidateModelDataDirectory(structure, f.dir)
		}
		if err := validate(); err != nil {
			t.Fatal(err)
		}
		delete(image, "output")
		requireModelDataError(t, validate(), "invalid output modalities")
		image["output"] = []string{"text"}
		requireModelDataError(t, validate(), "invalid output modalities")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:160
	t.Run("rejects output modalities on chat models", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.values["chat:model-a"].(map[string]any)["output"] = []string{"text"}
		f.write(t, ModelDataSchemaVersion, "openai-completions")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "unsupported output modalities")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:170
	t.Run("validates classifier models without chat output limits", func(t *testing.T) {
		f := newModelDataFixture(t)
		structure := ModelDataStructure{"test-provider": {"classifier:classifier-a": "test-classifier"}}
		classifier := map[string]any{"type": "classifier", "id": "classifier-a", "name": "Classifier A", "api": "test-classifier", "provider": "test-provider", "baseUrl": "https://example.test/v1", "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 1000}
		f.writeData(t, structure, map[string]any{"classifier:classifier-a": classifier}, ModelDataSchemaVersion, "test-classifier")
		if err := ValidateModelDataDirectory(structure, f.dir); err != nil {
			t.Fatal(err)
		}
	})
	// model-data.ts:279-281 (no upstream test): the key must be "<type>:<id>".
	t.Run("rejects a key that disagrees with the model's type and id", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.values["chat:model-a"].(map[string]any)["type"] = "classifier"
		f.write(t, ModelDataSchemaVersion, "openai-completions")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "chat:model-a has mismatched type/id identity")
	})
	// model-data.ts:173-194 (no upstream test): chat models need maxTokens and classifier models a contextWindow.
	t.Run("rejects chat models without maxTokens and classifiers without a context window", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.values["chat:model-a"].(map[string]any)["maxTokens"] = 0
		f.write(t, ModelDataSchemaVersion, "openai-completions")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "has invalid maxTokens")
		structure := ModelDataStructure{"test-provider": {"classifier:classifier-a": "test-classifier"}}
		classifier := map[string]any{"type": "classifier", "id": "classifier-a", "name": "Classifier A", "api": "test-classifier", "provider": "test-provider", "baseUrl": "https://example.test/v1", "input": []string{"text"}, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
		f.writeData(t, structure, map[string]any{"classifier:classifier-a": classifier}, ModelDataSchemaVersion, "test-classifier")
		requireModelDataError(t, ValidateModelDataDirectory(structure, f.dir), "has invalid contextWindow")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:196
	t.Run("rejects a model in the wrong API group", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.write(t, ModelDataSchemaVersion, "anthropic-messages")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "grouped under API")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:208
	t.Run("rejects duplicate model IDs across API groups", func(t *testing.T) {
		f := newModelDataFixture(t)
		content := `{"openai-completions":` + strings.TrimSpace(modelFixtureJSON(t, f.values)) + `,"anthropic-messages":` + strings.TrimSpace(modelFixtureJSON(t, f.values)) + "}\n"
		writeModelFixture(t, filepath.Join(f.dir, "test-provider.json"), content)
		manifest := CreateModelDataManifest(f.structure, map[string]string{"test-provider.json": content}, fixtureGeneratedAt)
		writeModelFixture(t, filepath.Join(f.dir, ModelDataManifestFile), modelFixtureJSON(t, manifest))
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "more than one API group")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:221
	t.Run("rejects missing model IDs and stale file hashes", func(t *testing.T) {
		f := newModelDataFixture(t)
		writeModelFixture(t, filepath.Join(f.dir, "test-provider.json"), "{}\n")
		err := ValidateModelDataDirectory(f.structure, f.dir)
		if err == nil || !(strings.Contains(err.Error(), "manifest hash") || strings.Contains(err.Error(), "model IDs")) {
			t.Fatalf("error = %v", err)
		}
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:227
	t.Run("rejects incompatible schema and generation stamps", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.write(t, ModelDataSchemaVersion+1, "openai-completions")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "model data schema")
		f.mutateManifest(t, "structureHash", "stale")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "generation stamp")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:239
	t.Run("rejects an invalid generation timestamp", func(t *testing.T) {
		f := newModelDataFixture(t)
		f.mutateManifest(t, "generatedAt", "invalid")
		requireModelDataError(t, ValidateModelDataDirectory(f.structure, f.dir), "generation timestamp")
	})
	// .upstream/v0.99.1/packages/ai/test/model-data-validation.test.ts:248
	t.Run("rejects missing provider shards imported by the aggregator", func(t *testing.T) {
		f := newModelDataFixture(t)
		writeModelFixture(t, filepath.Join(f.root, "src", "models.generated.ts"), "import { TEST_PROVIDER_CLASSIFIER_MODELS, TEST_PROVIDER_IMAGE_MODELS, TEST_PROVIDER_MODELS } from \"./providers/test-provider.models.ts\";\nimport { MISSING_CLASSIFIER_MODELS, MISSING_IMAGE_MODELS, MISSING_MODELS } from \"./providers/missing.models.ts\";\n")
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
