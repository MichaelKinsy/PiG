package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRenderPublishedClassifierCatalog(t *testing.T) {
	src := filepath.Join(t.TempDir(), "models.generated.mjs")
	data := `export const CLASSIFIER_MODELS = {
  zed: { "z/2": { type: "classifier", id: "z/2", name: "Z2", api: "system", provider: "zed", baseUrl: "https://z", input: ["text"], cost: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4 }, contextWindow: 10 },
         "a/1": { type: "classifier", id: "a/1", name: "A1", api: "system", provider: "zed", baseUrl: "https://z", input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 20 } },
  alpha: { a: { type: "classifier", id: "a", name: "A", api: "system", provider: "alpha", baseUrl: "https://a", headers: { z: "2", a: "1" }, input: ["text"], cost: { input: 0, output: 1, cacheRead: 0, cacheWrite: 0 }, contextWindow: 30 } },
  empty: {}
};`
	if err := os.WriteFile(src, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	models, err := load(src)
	if err != nil {
		t.Fatal(err)
	}
	// Providers are sorted; a provider's models keep the published order.
	if len(models) != 3 || models[0].ID != "a" || models[1].ID != "z/2" || models[2].ID != "a/1" {
		t.Fatalf("models = %#v", models)
	}
	generated, err := render(models)
	if err != nil {
		t.Fatal(err)
	}
	text := string(generated)
	if !strings.Contains(text, `"a": "1"`) || strings.Index(text, `"z/2"`) > strings.Index(text, `"a/1"`) {
		t.Fatalf("generated catalog is not deterministic:\n%s", text)
	}
}
