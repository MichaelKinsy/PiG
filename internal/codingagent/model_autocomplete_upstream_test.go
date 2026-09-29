package codingagent

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// .upstream/v0.87.1/packages/coding-agent/test/interactive-mode-status.test.ts:404
func TestInteractiveModelArgumentCompletionUpstream(t *testing.T) {
	for _, provider := range ai.ListProviders() {
		for _, key := range ai.FindEnvKeys(provider, nil) {
			t.Setenv(key, "")
		}
	}
	dir := t.TempDir()
	registry := NewModelRegistry(dir)
	auth, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry.SetAuthStorage(auth)
	for _, model := range []RuntimeModel{{Provider: "github-copilot", ID: "gpt-5.2-codex", Name: "GPT-5.2 Codex"}, {Provider: "openai-codex", ID: "gpt-5.5", Name: "GPT-5.5"}} {
		registry.RegisterProvider(model.Provider, extension.ProviderConfig{APIKey: "fixture-key", Models: []extension.ProviderModelConfig{{ID: model.ID, Name: model.Name}}})
	}
	mode := &InteractiveMode{opts: InteractiveOptions{AgentDir: dir, ModelRegistry: registry}}
	line := "/model codexgpt"
	suggestions := mode.buildAutocompleteProvider().GetSuggestions([]string{line}, 0, len(line))
	var values []string
	if suggestions != nil {
		for _, item := range suggestions.Items {
			values = append(values, item.Value)
		}
	}
	if !reflect.DeepEqual(values, []string{"openai-codex/gpt-5.5", "github-copilot/gpt-5.2-codex"}) {
		t.Fatalf("model argument values=%q", values)
	}
}
