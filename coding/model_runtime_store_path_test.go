package coding

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: model-runtime.ts CreateModelRuntimeOptions.modelsStorePath is the file the model catalogs persist to; the first refresh restores
// cached catalogs from it without network access. A stored remote catalog applies only when its Last-Modified is later than the bundled
// catalogs' generation time (model-runtime.ts:225, remote-catalog-provider.ts:52-57), so the fixture entry is newer than it.
func TestCreateModelRuntimeRestoresCachedCatalogsFromModelsStorePath(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PI_OFFLINE", "")
	storePath := filepath.Join(t.TempDir(), "custom-store.json")
	record := json.RawMessage(`{"id":"cached-model","name":"Cached","api":"openai-completions","provider":"openrouter","baseUrl":"https://openrouter.ai/api/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":1000,"maxTokens":100}`)
	generatedAt := ai.GetBuiltinModelDataGeneratedAt()
	if generatedAt == nil {
		t.Fatal("the generated catalogs record no generation time")
	}
	entry := ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{record}), CheckedAt: new(float64(1)), LastModified: new(*generatedAt + 1)}
	if err := ai.NewFileModelsStore(storePath).Write(t.Context(), "openrouter", entry); err != nil {
		t.Fatal(err)
	}
	modelsPath := filepath.Join(t.TempDir(), "models.json")
	create := func(options CreateModelRuntimeOptions) *ModelRuntime {
		t.Helper()
		options.Credentials = ai.NewInMemoryCredentialStore()
		options.ModelsPath = new(new(modelsPath))
		runtime, err := CreateModelRuntime(t.Context(), options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(runtime.Close)
		return runtime
	}

	if create(CreateModelRuntimeOptions{ModelsStorePath: storePath}).GetModel("openrouter", "cached-model") == nil {
		t.Fatal("the model cached in ModelsStorePath was not restored by the first refresh")
	}
	if create(CreateModelRuntimeOptions{ModelsStore: ai.NewInMemoryModelsStore()}).GetModel("openrouter", "cached-model") != nil {
		t.Fatal("another store returned the model cached in ModelsStorePath")
	}
}
