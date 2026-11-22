package providers

import (
	"strings"
	"testing"

	. "github.com/MichaelKinsy/PiG/internal/modelgen"
)

// Ports Pi fcc33744b (#12269): GPT-6 models on openai-codex set compat.supportsMidConvoEffort: true
// via the shared OpenAIMidConvoEffortModelIDs set in family_openai.go, while GPT-5.6 models do not.
func TestOpenAICodexMidConvoEffortCompat(t *testing.T) {
	catalog, err := provBBuild(t, "openai-codex", &CatalogSources{}, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := provBModelsOf(catalog)

	gpt6 := []string{"gpt-6-astra", "gpt-6-sol", "gpt-6-luna", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6.2-astra", "gpt-6.2-sol"}
	for _, id := range gpt6 {
		m, ok := byID[id]
		if !ok {
			t.Fatalf("missing openai-codex model %q", id)
		}
		if compat := provBField(m, "compat"); !strings.Contains(compat, `"supportsMidConvoEffort":true`) {
			t.Errorf("openai-codex/%s compat = %s, want supportsMidConvoEffort:true", id, compat)
		}
	}

	gpt56 := []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"}
	for _, id := range gpt56 {
		m, ok := byID[id]
		if !ok {
			t.Fatalf("missing openai-codex model %q", id)
		}
		if compat := provBField(m, "compat"); strings.Contains(compat, "supportsMidConvoEffort") {
			t.Errorf("openai-codex/%s compat = %s, want supportsMidConvoEffort omitted", id, compat)
		}
	}
}
