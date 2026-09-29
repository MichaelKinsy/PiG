package codingagent

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func BenchmarkCopilotAccountAvailability(b *testing.B) {
	dir := b.TempDir()
	store, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		b.Fatal(err)
	}
	models := ai.ListModels("github-copilot")
	ids, err := json.Marshal([]string{models[0].ID})
	if err != nil {
		b.Fatal(err)
	}
	if err := store.Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Access: "fixture", AvailableModelIDs: ids}); err != nil {
		b.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	registry.SetAuthStorage(store)
	b.ReportAllocs()
	for b.Loop() {
		_ = registry.GetAvailableModelData()
	}
}
