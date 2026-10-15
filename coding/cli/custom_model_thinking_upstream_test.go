package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestCustomModelThinkingSurvivesStartupConstruction(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/src/core/model-resolver.ts:571-595
	for _, api := range []string{"openai-completions", "openai-responses"} {
		t.Run(api, func(t *testing.T) {
			dir := t.TempDir()
			config := `{"providers":{"neuralwatt":{"api":"` + api + `","baseUrl":"https://fixture.invalid/v1","apiKey":"test-key","models":[{"id":"some-base-model","reasoning":false}]}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := selectStartupModel(t.Context(), startupModelOptions{CLIModel: "neuralwatt/zai-org/GLM-5.1-FP8:high"}, codingagent.Settings{}, testServices(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			if got.Model == nil || got.Model.ID != "zai-org/GLM-5.1-FP8" || !got.Model.ProviderMeta.Reasoning || got.Thinking != "high" {
				t.Fatalf("startup model=%+v, thinking=%q", got.Model, got.Thinking)
			}
		})
	}
}
