package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// TestModelsJSONReadsIntegerValuedFloats pins JSON.parse number semantics for models.json: ModelDefinitionSchema's contextWindow and maxTokens are
// Type.Number() (model-config.ts:210-211), and JSON.parse reads 128000.0 and 4.096e3 as 128000 and 4096. encoding/json refused them for the int fields,
// and the whole file failed to load.
func TestModelsJSONReadsIntegerValuedFloats(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	config := `{"providers":{"floaty":{"baseUrl":"https://floaty.example/v1","apiKey":"k","api":"openai-completions","models":[{"id":"m","contextWindow":128000.0,"maxTokens":4.096e3}]}}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistryWithModelsPath(path)
	t.Cleanup(registry.CloseModelTasks)
	if loadErr := registry.LoadError(); loadErr != "" {
		t.Fatalf("load error %q", loadErr)
	}
	entry, ok := registry.Resolve("floaty", "m")
	if !ok {
		t.Fatal("floaty/m is not registered")
	}
	if entry.ContextWindow != 128000 || entry.MaxTokens != 4096 {
		t.Fatalf("contextWindow %d maxTokens %d, want 128000 and 4096", entry.ContextWindow, entry.MaxTokens)
	}
}
