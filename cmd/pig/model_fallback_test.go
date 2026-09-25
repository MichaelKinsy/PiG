package main

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestBuildModelParsesMaxThinkingSuffix(t *testing.T) {
	agentDirForModelOverride = t.TempDir()
	t.Cleanup(func() { agentDirForModelOverride = "" })
	registry := codingagent.NewModelRegistry(t.TempDir())

	model, thinking, _, err := buildModel("github-copilot/gpt-5.4:max", registry)
	if err != nil {
		t.Fatalf("buildModel: %v", err)
	}
	if model.ID != "gpt-5.4" || thinking != "max" {
		t.Fatalf("model/thinking = %q/%q, want gpt-5.4/max", model.ID, thinking)
	}
}

// An exact provider/model catalog entry resolves with its real capabilities
// and no warning.
func TestResolveModel_ExactCatalog_NoWarning(t *testing.T) {
	agentDirForModelOverride = t.TempDir()
	t.Cleanup(func() { agentDirForModelOverride = "" })
	registry := codingagent.NewModelRegistry(t.TempDir())

	model, _, warning, err := resolveModel("github-copilot/gpt-5.4", "", codingagent.Settings{}, registry)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	if warning != "" {
		t.Errorf("warning = %q, want empty for a catalogued model", warning)
	}
	if model.Capabilities.ContextWindow != 1000000 {
		t.Errorf("context window = %d, want 1000000 (copilot gpt-5.4)", model.Capabilities.ContextWindow)
	}
}

// An unknown model under a known provider falls back to that provider's
// default model capabilities (NOT another provider's same-named model) and
// emits the upstream "not found for provider … Using custom model id."
// warning. Guards against the cross-provider LookupModel borrow that made
// `github-copilot/gpt-4o` silently inherit openai gpt-4o's 128k window.
func TestResolveModel_UnknownUnderProvider_FallsBackAndWarns(t *testing.T) {
	agentDirForModelOverride = t.TempDir()
	t.Cleanup(func() { agentDirForModelOverride = "" })
	registry := codingagent.NewModelRegistry(t.TempDir())

	model, _, warning, err := resolveModel("github-copilot/gpt-4o", "", codingagent.Settings{}, registry)
	if err != nil {
		t.Fatalf("resolveModel: %v", err)
	}
	wantWarn := `Model "gpt-4o" not found for provider "github-copilot". Using custom model id.`
	if warning != wantWarn {
		t.Errorf("warning = %q, want %q", warning, wantWarn)
	}
	if model.ID != "gpt-4o" || model.DisplayName != "gpt-4o" {
		t.Errorf("model id/name = %q/%q, want gpt-4o/gpt-4o", model.ID, model.DisplayName)
	}
	copilotDefault, ok := ai.LookupModelExact("github-copilot/" + defaultModelPerProvider()["github-copilot"])
	if !ok {
		t.Fatal("copilot default model missing from catalog")
	}
	if model.Capabilities.ContextWindow != copilotDefault.ContextWindow {
		t.Errorf("context window = %d, want %d (copilot default model, not a cross-provider borrow)",
			model.Capabilities.ContextWindow, copilotDefault.ContextWindow)
	}
	if model.Capabilities.ContextWindow == 128000 {
		t.Error("context window is 128000: the cross-provider openai gpt-4o borrow regressed")
	}
}
