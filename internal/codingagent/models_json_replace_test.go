package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi docs/models.md: "A `models` entry adds or replaces a model with the same ID on that provider. Use `modelOverrides` to change metadata
// for an existing built-in or extension-provided model without replacing the provider's model list." Pi #9566 asked for the replaced model's
// values to carry over; the maintainer answered with modelOverrides, so PiG keeps Pi's replacement (provider-composer.ts modelFromJson defaults).
func TestModelsJSONEntryForAnExposedModelReplacesItAndModelOverridesKeepItsFields(t *testing.T) {
	load := func(t *testing.T, provider string) *ai.Model {
		t.Helper()
		path := filepath.Join(t.TempDir(), "models.json")
		if err := os.WriteFile(path, []byte(`{"providers":{"azure":`+provider+`}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		r := NewModelRegistryWithModelsPath(path)
		if r.LoadError() != "" {
			t.Fatal(r.LoadError())
		}
		models := r.GetProviderModelData("azure")
		i := slices.IndexFunc(models, func(m *ai.Model) bool { return m.ID == "gpt-5.4" })
		if i < 0 {
			t.Fatal("gpt-5.4 missing")
		}
		return models[i]
	}
	builtin := load(t, `{"baseUrl":"https://example.test/v1","modelOverrides":{}}`)
	if builtin.Capabilities.ContextWindow == 128000 {
		t.Fatalf("fixture model unusable: %+v", builtin)
	}
	replaced := load(t, `{"baseUrl":"https://example.test/v1","models":[{"id":"gpt-5.4"}]}`)
	if replaced.Capabilities.ContextWindow != 128000 || replaced.Capabilities.MaxOutputTokens != 16384 {
		t.Errorf("a models entry replaces the model with the defaults: %d/%d", replaced.Capabilities.ContextWindow, replaced.Capabilities.MaxOutputTokens)
	}
	overridden := load(t, `{"baseUrl":"https://example.test/v1","modelOverrides":{"gpt-5.4":{"name":"Mine"}}}`)
	if overridden.DisplayName != "Mine" || overridden.Capabilities.ContextWindow != builtin.Capabilities.ContextWindow || overridden.Capabilities.MaxOutputTokens != builtin.Capabilities.MaxOutputTokens {
		t.Errorf("modelOverrides keeps the exposed model's limits: %+v", overridden)
	}
}
