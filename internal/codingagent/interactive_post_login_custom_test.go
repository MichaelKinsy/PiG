package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi interactive-mode.ts:5903-5904 reports missing defaults even for a custom
// OpenAI-compatible provider with available models; it does not choose its first model.
func TestAPIKeyLoginCustomEndpointDoesNotInventDefault(t *testing.T) {
	m := newPostLoginTestMode(t)
	const endpoint = "https://custom-provider.invalid/v1"
	data := `{"providers":{"custom-provider":{"api":"openai-completions","baseUrl":"` + endpoint + `","models":[{"id":"custom-model"}]}}}`
	if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "models.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.ModelRegistry.Refresh()
	m.opts.ModelBuilder = func(string) (*ai.Model, error) {
		t.Error("login must not invent a default for a custom endpoint")
		return nil, nil
	}
	if err := setPostLoginAPIKey(m, "custom-provider", "synthetic-custom-key"); err != nil {
		t.Fatal(err)
	}
	waitPostLoginStatus(t, m, `no default model is configured for provider "custom-provider"`)
	entry, ok := m.opts.ModelRegistry.Resolve("custom-provider", "custom-model")
	if !ok || entry.BaseURL != endpoint || entry.API != "openai-completions" {
		t.Fatalf("login changed custom endpoint: %+v", entry)
	}
	if m.opts.Model != nil || m.opts.SettingsManager.GetDefaultModel() != "" {
		t.Fatal("login selected or persisted an invented default")
	}
	if got := plainRender(m.chatContainer); !strings.Contains(got, "Use /model to select a model.") {
		t.Fatalf("missing Pi selection guidance: %s", got)
	}
}
