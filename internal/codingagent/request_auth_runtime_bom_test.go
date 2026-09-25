package codingagent

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestRequestAuthRuntimePreservesBOMProviderOrder(t *testing.T) {
	dir := t.TempDir()
	data := "\ufeff" + `{"providers":{"z-first":{"baseUrl":"https://example.invalid","apiKey":"test"},"a-second":{"baseUrl":"https://example.invalid","apiKey":"test"}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRequestAuthRuntime(context.Background(), RequestAuthRuntimeOptions{Credentials: ai.NewInMemoryAuthStorage(nil), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, provider := range runtime.GetProviders() {
		if provider.ID == "z-first" || provider.ID == "a-second" {
			ids = append(ids, provider.ID)
		}
	}
	if !slices.Equal(ids, []string{"z-first", "a-second"}) {
		t.Fatalf("provider order = %v", ids)
	}
}
