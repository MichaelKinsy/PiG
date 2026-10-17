//go:build !race

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// modelsReadObjectsCeiling bounds one more read of a small models.json after the first: Pi's ModelConfig.load validates against the
// ModelsConfigSchema value model-config.ts built once at module load, so a re-read parses and validates the document and builds no
// schema. It measured 187 objects. Building the tree for every read cost 3,019 objects a read, and startup reads models.json
// twice (NewModelRegistry and the first model refresh).
const modelsReadObjectsCeiling = 220

func TestModelsConfigReadBuildsSchemaOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	models := `{"providers":{"fake":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"fake","models":[{"id":"fake-model","contextWindow":200000,"maxTokens":8192}]}}}`
	if err := os.WriteFile(path, []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistryWithModelsPath(path)
	if registry.loadError != "" {
		t.Fatalf("load error: %s", registry.loadError)
	}
	objects := testing.AllocsPerRun(20, func() {
		if _, loadError := registry.readConfig(); loadError != "" {
			t.Fatalf("read error: %s", loadError)
		}
	})
	t.Logf("a models.json re-read allocates %.0f objects", objects)
	if objects > modelsReadObjectsCeiling {
		t.Errorf("a models.json re-read allocates %.0f objects, ceiling %d", objects, modelsReadObjectsCeiling)
	}
}
