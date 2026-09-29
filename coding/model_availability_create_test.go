package coding

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi model-runtime.ts:211-220 skips both catalog and availability work when refreshOnCreate is false.
func TestCreateModelRuntimeSkipsAvailabilityWhenRefreshDisabled(t *testing.T) {
	dir := t.TempDir()
	modelsPath := filepath.Join(dir, "models.json")
	if err := os.WriteFile(modelsPath, []byte(`{"providers":{"creation-faux":{"api":"openai-completions","baseUrl":"https://custom.invalid/v1","apiKey":"configured","models":[{"id":"visible"}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, refresh := range []bool{false, true} {
		runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{AuthPath: filepath.Join(dir, "auth.json"), ModelsPath: new(&modelsPath), RefreshOnCreate: new(refresh)})
		if err != nil {
			t.Fatal(err)
		}
		available := slices.ContainsFunc(runtime.GetAvailableSnapshot(), func(model *ai.Model) bool { return model.ProviderMeta.ProviderID == "creation-faux" })
		if available != refresh {
			t.Fatalf("refreshOnCreate=%v available=%v", refresh, available)
		}
		if runtime.GetModel("creation-faux", "visible") == nil {
			t.Fatal("skipping refresh discarded static metadata")
		}
	}
}
