package main

// pi: packages/ai/scripts/hydrate-model-catalog.ts

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Ports .upstream/v1.0.1/packages/ai/test/model-data-validation.test.ts ("published model catalog hydration").

func (f modelDataFixture) chatModel() map[string]any {
	model := map[string]any{}
	maps.Copy(model, f.values["chat:model-a"].(map[string]any))
	return model
}

func (f modelDataFixture) writeCatalog(t *testing.T, catalog any) string {
	t.Helper()
	path := filepath.Join(f.root, "catalog.json")
	writeModelFixture(t, path, modelFixtureJSON(t, catalog))
	return path
}

func readModelFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func with(model map[string]any, key string, value any) map[string]any {
	out := map[string]any{}
	maps.Copy(out, model)
	out[key] = value
	return out
}

func TestHydrateModelCatalogUpstream(t *testing.T) {
	t.Run("hydrates a clean checkout deterministically without changing generated TypeScript", func(t *testing.T) {
		f := newModelDataFixture(t)
		aggregatorPath := filepath.Join(f.root, "src", "models.generated.ts")
		aggregator := readModelFile(t, aggregatorPath)
		models := []any{f.chatModel()}
		catalog := f.writeCatalog(t, map[string]any{"test-provider": models, "extra-provider": models})
		if err := os.RemoveAll(f.dir); err != nil {
			t.Fatal(err)
		}
		if err := HydrateModelCatalog(f.root, catalog, false); err != nil {
			t.Fatal(err)
		}
		if err := ValidateModelDataDirectory(f.structure, f.dir); err != nil {
			t.Fatal(err)
		}
		if got, err := ReadModelDataStructure(f.root); err != nil || !reflect.DeepEqual(got, f.structure) {
			t.Fatalf("structure = %v, %v", got, err)
		}
		if readModelFile(t, aggregatorPath) != aggregator {
			t.Fatal("aggregator changed")
		}
		manifest := readModelFile(t, filepath.Join(f.dir, ModelDataManifestFile))
		provider := readModelFile(t, filepath.Join(f.dir, "test-provider.json"))
		if err := HydrateModelCatalog(f.root, catalog, false); err != nil {
			t.Fatal(err)
		}
		if readModelFile(t, filepath.Join(f.dir, ModelDataManifestFile)) != manifest || readModelFile(t, filepath.Join(f.dir, "test-provider.json")) != provider {
			t.Fatal("hydration is not deterministic")
		}
	})
	t.Run("groups models by API and keys them by type and id", func(t *testing.T) {
		f := newModelDataFixture(t)
		model := f.chatModel()
		b := with(with(model, "id", "model-b"), "api", "anthropic-messages")
		if err := HydrateModelCatalog(f.root, f.writeCatalog(t, map[string]any{"test-provider": []any{model, b}}), false); err != nil {
			t.Fatal(err)
		}
		want := ModelDataStructure{"test-provider": {"chat:model-a": "openai-completions", "chat:model-b": "anthropic-messages"}}
		if got, err := ReadModelDataStructure(f.root); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("structure = %v, %v", got, err)
		}
	})
	t.Run("keys chat and image models with the same id separately", func(t *testing.T) {
		f := newModelDataFixture(t)
		image := map[string]any{"type": "image", "id": "model-a", "name": "Model A Image", "api": "openrouter-images", "provider": "test-provider", "baseUrl": "https://example.test/v1", "input": []string{"text"}, "output": []string{"image"}, "cost": map[string]any{"input": 1, "output": 2, "cacheRead": 0, "cacheWrite": 0}}
		if err := HydrateModelCatalog(f.root, f.writeCatalog(t, map[string]any{"test-provider": []any{f.chatModel(), image}}), false); err != nil {
			t.Fatal(err)
		}
		want := ModelDataStructure{"test-provider": {"chat:model-a": "openai-completions", "image:model-a": "openrouter-images"}}
		if got, err := ReadModelDataStructure(f.root); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("structure = %v, %v", got, err)
		}
	})
	t.Run("validates without replacing existing data", func(t *testing.T) {
		f := newModelDataFixture(t)
		original := readModelFile(t, filepath.Join(f.dir, "test-provider.json"))
		model := f.chatModel()
		if err := HydrateModelCatalog(f.root, f.writeCatalog(t, map[string]any{"test-provider": []any{model, with(model, "id", "model-b")}}), true); err != nil {
			t.Fatal(err)
		}
		if readModelFile(t, filepath.Join(f.dir, "test-provider.json")) != original {
			t.Fatal("validation replaced the data")
		}
		requireModelDataError(t, HydrateModelCatalog(f.root, f.writeCatalog(t, map[string]any{"other-provider": []any{model}}), true), "missing provider")
	})
	for _, catalog := range []string{`null`, `[]`, `{}`, `{"test-provider":[]}`, `{"test-provider":{}}`, `{"test-provider":[{"id":"x"}]}`} {
		t.Run("rejects incomplete catalogs without replacing existing data: "+catalog, func(t *testing.T) {
			f := newModelDataFixture(t)
			original := readModelFile(t, filepath.Join(f.dir, ModelDataManifestFile))
			path := filepath.Join(f.root, "catalog.json")
			writeModelFixture(t, path, catalog)
			if err := HydrateModelCatalog(f.root, path, false); err == nil {
				t.Fatal("hydration succeeded")
			}
			if readModelFile(t, filepath.Join(f.dir, ModelDataManifestFile)) != original {
				t.Fatal("data replaced")
			}
		})
	}
	for _, field := range []string{"api", "provider", "cost"} {
		t.Run("validates model "+field+" before replacing existing data", func(t *testing.T) {
			f := newModelDataFixture(t)
			original := readModelFile(t, filepath.Join(f.dir, "test-provider.json"))
			if err := HydrateModelCatalog(f.root, f.writeCatalog(t, map[string]any{"test-provider": []any{with(f.chatModel(), field, nil)}}), false); err == nil {
				t.Fatal("hydration succeeded")
			}
			if readModelFile(t, filepath.Join(f.dir, "test-provider.json")) != original {
				t.Fatal("data replaced")
			}
		})
	}
}

// groupProviderModelData rejects two entries with the same type and id in one API group.
func TestGroupProviderModelDataRejectsDuplicates(t *testing.T) {
	entry := ModelCatalogEntry{Type: "chat", ID: "a", API: "x", JSON: json.RawMessage(`{}`)}
	if _, _, err := GroupProviderModelData("p", []ModelCatalogEntry{entry, entry}); err == nil || err.Error() != "p/chat:a has duplicate x catalog entries" {
		t.Fatalf("err = %v", err)
	}
}

// Pins the fixed manifest stamp of packages/ai/scripts/hydrate-model-catalog.ts:65 ("1970-01-01T00:00:00.000Z"); the upstream hydration tests do not assert it, but identical catalogs only hydrate to identical bytes because of it.
func TestHydrateModelCatalogStampsTheEpochGeneratedAt(t *testing.T) {
	f := newModelDataFixture(t)
	if err := HydrateModelCatalog(f.root, f.writeCatalog(t, map[string]any{"test-provider": []any{f.chatModel()}}), false); err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		GeneratedAt string `json:"generatedAt"`
	}
	if err := json.Unmarshal([]byte(readModelFile(t, filepath.Join(f.dir, ModelDataManifestFile))), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.GeneratedAt != "1970-01-01T00:00:00.000Z" {
		t.Fatalf("generatedAt = %q, want the epoch stamp", manifest.GeneratedAt)
	}
}
