package cli

import (
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

func TestExtensionScopedModelsEmptyDoesNotReadCatalog(t *testing.T) {
	// Pi main.ts only calls resolveModelScope when modelPatterns.length > 0.
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	var reads atomic.Int64
	provider := &ai.ModelsProvider{ID: "scope-probe", GetModels: func() ([]*ai.Model, error) {
		reads.Add(1)
		return nil, nil
	}}
	if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	for _, patterns := range [][]string{nil, {}} {
		before := reads.Load()
		if models := extensionScopedModels(services, patterns); len(models) != 0 {
			t.Fatalf("empty scope returned models: %v", models)
		}
		if got := reads.Load() - before; got != 0 {
			t.Fatalf("empty scope read a provider catalog %d times", got)
		}
	}
}

func TestExtensionScopedModelsExplicitSelection(t *testing.T) {
	dir := isolateProviderAuthEnv(t)
	config := `{"providers":{"scope-local":{"api":"openai-completions","baseUrl":"https://scope.invalid/v1","apiKey":"fixture-key","models":[{"id":"first","reasoning":true},{"id":"second","reasoning":true}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	for _, tc := range []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"no match", []string{"missing-scope-model"}, nil},
		{"one", []string{"scope-local/first:high"}, []string{"first:high"}},
		{"ordered and deduplicated", []string{"scope-local/second:low", "scope-local/first:high", "scope-local/second:low"}, []string{"second:low", "first:high"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []string
			for _, scoped := range extensionScopedModels(services, tc.patterns) {
				model := scoped.Model
				if model.ProviderMeta.ProviderID != "scope-local" || model.ProviderMeta.API != ai.APIOpenAICompletions || model.ProviderMeta.BaseURL != "https://scope.invalid/v1" {
					t.Fatalf("scope lost provider metadata: %+v", model.ProviderMeta)
				}
				got = append(got, model.ID+":"+string(scoped.ThinkingLevel))
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("scope = %v, want %v", got, tc.want)
			}
		})
	}
}

func BenchmarkExtensionScopedModelsEmpty(b *testing.B) {
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	b.ReportAllocs()
	for b.Loop() {
		if models := extensionScopedModels(services, nil); len(models) != 0 {
			b.Fatal(models)
		}
	}
}
