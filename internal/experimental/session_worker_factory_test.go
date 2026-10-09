package experimental

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
)

// upstream: packages/coding-agent/src/experimental/durable/harness-setup.ts:93-124 and session-worker.ts:797-806. A new root conversation starts with the model of an explicit --provider/--model, or with pi's default resolution for a new session; the thinking level is the explicit suffix, the saved default, or unset.
func TestFindInitialAgentModelUsesNativeModelsAndSettings(t *testing.T) {
	for _, api := range []string{"openai-completions", "openai-responses"} {
		for _, selection := range []struct {
			name, provider, model string
			wantThinking          string
		}{
			{name: "saved default", wantThinking: "low"},
			{name: "explicit model", provider: "worker-factory", model: "model"},
			{name: "explicit thinking suffix", model: "worker-factory/model:high", wantThinking: "high"},
		} {
			t.Run(api+"/"+selection.name, func(t *testing.T) {
				agentDir := isolateExperimentalTest(t)
				modelConfig := map[string]any{"providers": map[string]any{"worker-factory": map[string]any{
					"baseUrl": "https://worker-factory.invalid/v1", "api": api, "apiKey": "fixture-key",
					"models": []any{map[string]any{"id": "model", "name": "Factory model", "reasoning": true, "input": []string{"text", "image"}, "contextWindow": 32000, "maxTokens": 1234}},
				}}}
				encoded, err := json.Marshal(modelConfig)
				if err != nil {
					t.Fatal(err)
				}
				writeNodeFacetFile(t, filepath.Join(agentDir, "models.json"), string(encoded))
				writeNodeFacetFile(t, filepath.Join(agentDir, "settings.json"), `{"defaultProvider":"worker-factory","defaultModel":"model","defaultThinkingLevel":"low"}`)
				collaborators, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				modelRuntime := collaborators.ModelRuntime()
				_ = modelRuntime.Refresh(context.Background(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
				initial, err := durableagent.FindInitialAgentModel(durableagent.ModelSelection{Runtime: modelRuntime, ConfiguredAuth: collaborators.Registry().ModelRegistry.HasConfiguredAuth}, collaborators.SettingsManager(), selection.provider, selection.model)
				if err != nil {
					t.Fatal(err)
				}
				if initial.Model == nil || *initial.Model != (durable.ModelRef{Provider: "worker-factory", ModelId: "model"}) || initial.ThinkingLevel != selection.wantThinking {
					t.Fatalf("initial = %+v; want worker-factory/model with thinking %q", initial, selection.wantThinking)
				}
				model := modelRuntime.GetModel("worker-factory", "model")
				if model == nil || string(model.ProviderMeta.API) != api || model.ProviderMeta.BaseURL != "https://worker-factory.invalid/v1" || model.DisplayName != "Factory model" || model.Capabilities.ContextWindow != 32000 || model.Capabilities.MaxOutputTokens != 1234 {
					t.Fatalf("native model = %+v", model)
				}
			})
		}
	}
}

// harness-setup.ts:98-103: an explicit model that does not resolve fails with Pi's message and no fallback.
func TestFindInitialAgentModelRejectsAnUnresolvableExplicitModel(t *testing.T) {
	isolateExperimentalTest(t)
	collaborators, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = durableagent.FindInitialAgentModel(durableagent.ModelSelection{Runtime: collaborators.ModelRuntime(), ConfiguredAuth: collaborators.Registry().ModelRegistry.HasConfiguredAuth}, collaborators.SettingsManager(), "", "nowhere/missing")
	if err == nil || len(err.Error()) <= len("Could not resolve model: ") || err.Error()[:len("Could not resolve model: ")] != "Could not resolve model: " {
		t.Fatalf("error = %v; want Could not resolve model: ...", err)
	}
}
