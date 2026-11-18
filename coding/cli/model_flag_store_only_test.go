package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// storeOnlyCarbonAgentDir is the fixture of the carbon start: the models-store holds only github-copilot's gemini-4-carbon (newer than the
// bundled data), and models.json holds one other copilot model. Nothing else is written, so no network or sign-in is involved.
func storeOnlyCarbonAgentDir(t *testing.T) string {
	t.Helper()
	dir := isolateProviderAuthEnv(t)
	files := map[string]string{
		"models.json":       `{"providers":{"github-copilot":{"models":[{"id":"claude-fable-5.2","name":"Claude Fable 5.2","api":"anthropic-messages","reasoning":true,"input":["text","image"],"contextWindow":1000000,"maxTokens":128000,"cost":{"input":8,"output":40,"cacheRead":0.2,"cacheWrite":10}}]}}}`,
		"models-store.json": `{"github-copilot":{"models":[{"id":"gemini-4-carbon","name":"Gemini 4 Carbon","api":"openai-completions","provider":"github-copilot","baseUrl":"https://api.individual.githubcopilot.com","reasoning":true,"input":["text","image"],"cost":{"input":2,"output":12,"cacheRead":0.1,"cacheWrite":0},"contextWindow":1048576,"maxTokens":65536}],"checkedAt":4102444800000,"lastModified":4102444800000}}`,
		"settings.json":     `{}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestStoreOnlyModelFlagBindsItsOwnLimits checks that a real --model start of a store-only model binds that model's own window, output
// limit, API and thinking range, not the provider default's (gpt-6.1-sol: 1050000, 128000, openai-responses, xhigh).
func TestStoreOnlyModelFlagBindsItsOwnLimits(t *testing.T) {
	services := testServices(t, storeOnlyCarbonAgentDir(t))
	model, thinking, _, err := resolveModel("github-copilot/gemini-4-carbon", "", codingagent.Settings{}, services)
	if err != nil {
		t.Fatal(err)
	}
	if got := model.Capabilities.ContextWindow; got != 1048576 {
		t.Errorf("contextWindow = %d, want 1048576", got)
	}
	if got := model.Capabilities.MaxOutputTokens; got != 65536 {
		t.Errorf("maxOutput = %d, want 65536", got)
	}
	if got := string(model.ProviderMeta.API); got != "openai-completions" {
		t.Errorf("API = %q, want openai-completions", got)
	}
	levels := ai.GetSupportedThinkingLevels(model)
	if slices.Contains(levels, ai.ModelThinkingLevel(ai.ThinkingLevelXHigh)) || !slices.Contains(levels, ai.ModelThinkingLevel(ai.ThinkingLevelHigh)) {
		t.Errorf("thinking levels = %v, want off..high (no xhigh)", levels)
	}
	_ = thinking
}

// TestStoreOnlyModelFlagHasNoCustomIDWarning checks that the stored model is found, so startup does not call it a custom model id.
func TestStoreOnlyModelFlagHasNoCustomIDWarning(t *testing.T) {
	services := testServices(t, storeOnlyCarbonAgentDir(t))
	selected, err := selectStartupModel(context.Background(), startupModelOptions{CLIModel: "github-copilot/gemini-4-carbon"}, codingagent.Settings{}, services)
	if err != nil {
		t.Fatal(err)
	}
	for _, warning := range selected.Warnings {
		if strings.Contains(warning, "custom model id") || strings.Contains(warning, "not found") {
			t.Errorf("unexpected warning: %q", warning)
		}
	}
}

// TestStoreOnlyControlOpusFlagUnchanged checks the control: a bundled --model keeps its own catalog limits.
func TestStoreOnlyControlOpusFlagUnchanged(t *testing.T) {
	services := testServices(t, storeOnlyCarbonAgentDir(t))
	model, _, _, err := resolveModel("github-copilot/claude-opus-5.5", "", codingagent.Settings{}, services)
	if err != nil {
		t.Fatal(err)
	}
	if model.ID != "claude-opus-5.5" {
		t.Fatalf("model id = %q", model.ID)
	}
	if model.Capabilities.ContextWindow == 1048576 || model.Capabilities.ContextWindow == 1050000 {
		t.Errorf("opus control contextWindow = %d, want its own catalog value", model.Capabilities.ContextWindow)
	}
}
