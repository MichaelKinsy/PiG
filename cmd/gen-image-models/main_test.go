package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRenderPublishedImageCatalog(t *testing.T) {
	src := filepath.Join(t.TempDir(), "image-models.generated.mjs")
	data := `export const IMAGE_MODELS = {
  zed: { b: { id: "b", name: "B", api: "images", provider: "zed", baseUrl: "https://z", input: ["text"], output: ["image"], cost: { input: 1, output: 2, cacheRead: 3, cacheWrite: 4 } } },
  alpha: { a: { id: "a", name: "A", api: "images", provider: "alpha", baseUrl: "https://a", headers: { z: "2", a: "1" }, input: ["image"], output: ["image"], cost: { input: 0, output: 1, cacheRead: 0, cacheWrite: 0 } } }
};`
	if err := os.WriteFile(src, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	models, err := load(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[1].ID != "b" {
		t.Fatalf("models = %#v", models)
	}
	generated, err := render(models)
	if err != nil {
		t.Fatal(err)
	}
	text := string(generated)
	if strings.Index(text, `ID:       "a"`) > strings.Index(text, `ID:       "b"`) || !strings.Contains(text, `"a": "1"`) {
		t.Fatalf("generated catalog is not deterministic:\n%s", text)
	}
}
