package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's ModelDefinitionSchema (model-config.ts) has no `type`, and provider-composer.ts:210-249 modelFromJson builds every
// models.json model as a chat model member by member, so a "type" written in models.json neither reaches the model
// object nor exempts the definition from modelFromJson's api check.
func TestModelsJSONTypeIsIgnored(t *testing.T) {
	t.Run("a written type does not reach the entry", func(t *testing.T) {
		dir := t.TempDir()
		config := `{"providers":{"p":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","models":[{"id":"chat-1","type":"chat"},{"id":"image-1","type":"image"}]}}}`
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		registry := NewModelRegistry(dir)
		if err := registry.LoadError(); err != "" {
			t.Fatal(err)
		}
		for _, id := range []string{"chat-1", "image-1"} {
			entry, ok := registry.Resolve("p", id)
			if !ok {
				t.Fatalf("p/%s not resolved", id)
			}
			if entry.Type != "" {
				t.Errorf("p/%s: Type %q, want none", id, entry.Type)
			}
		}
	})
	t.Run("an image definition without api fails as a chat definition", func(t *testing.T) {
		dir := t.TempDir()
		config := `{"providers":{"p":{"baseUrl":"http://127.0.0.1:9/v1","apiKey":"k","models":[{"id":"i","type":"image"}]}}}`
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		want := `Provider "p": Provider p, model i: no "api" specified. Set at provider or model level.`
		if got := NewModelRegistry(dir).LoadError(); !strings.Contains(got, want) {
			t.Fatalf("LoadError = %q, want it to contain %q", got, want)
		}
	})
}

// A Radius baseline model carries the catalog's "type":"chat" (model-catalog.ts flattenModelCatalog keeps the member) and a
// gateway model carries what the gateway sent (radius-config.ts getRadiusModelsFromConfig spreads it); the registry entry
// keeps either.
func TestRadiusModelEntryKeepsTheModelType(t *testing.T) {
	for _, modelType := range []ai.ModelType{ai.ModelTypeChat, ""} {
		model := &ai.Model{Type: modelType, ID: "m", ProviderMeta: ai.ProviderMetadata{ProviderID: "radius"}}
		if got := radiusModelEntry(model).Type; got != modelType {
			t.Errorf("radiusModelEntry(Type %q).Type = %q", modelType, got)
		}
		if got := nativeModelFromEntry(radiusModelEntry(model)).Type; got != modelType {
			t.Errorf("model of radiusModelEntry(Type %q).Type = %q", modelType, got)
		}
	}
}
