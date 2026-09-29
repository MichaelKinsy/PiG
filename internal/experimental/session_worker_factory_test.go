package experimental

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	envpkg "github.com/MichaelKinsy/PiG/agent/harness/env"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/src/experimental/session-worker.ts:807-866. The coding factory selects the real Model Runtime, acquires the durable main lane, and restores the exact read/write/bash tool loadout.
func TestCodingWorkerFactoryUsesNativeModelsAndDurableLane(t *testing.T) {
	for _, api := range []string{"openai-completions", "openai-responses"} {
		for _, selection := range []struct {
			name, provider, model string
			thinking              ai.ThinkingLevel
		}{
			{name: "saved default", thinking: ai.ThinkingLow},
			{name: "explicit model", provider: "worker-factory", model: "model", thinking: ai.ThinkingOff},
			{name: "explicit thinking suffix", model: "worker-factory/model:high", thinking: ai.ThinkingHigh},
		} {
			t.Run(api+"/"+selection.name, func(t *testing.T) {
				agentDir := isolateExperimentalTest(t)
				root := t.TempDir()
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
				executionEnv := envpkg.NewNodeExecutionEnv(envpkg.NodeExecutionEnvOptions{Cwd: root})
				t.Cleanup(func() { executionEnv.Cleanup(context.Background()) })
				sessionDir := filepath.Join(root, "sessions")
				repo := session.NewJsonlSessionRepo(session.JsonlSessionRepoOptions{FileSystem: executionEnv, SessionsRoot: sessionDir})
				t.Cleanup(func() {
					if err := repo.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				stored, err := repo.Create(t.Context(), session.SessionCreateOptions{ID: "factory-session", Cwd: root})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := stored.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				options := SessionWorkerOptions{SessionDir: sessionDir, Metadata: stored.Metadata(), Provider: selection.provider, Model: selection.model, PluginManifestPaths: []string{}}
				worker, err := createCodingAgentHarness(t.Context(), stored, options, executionEnv)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := worker.Harness.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				lane, ok := worker.Lane.(codingWorkerLane)
				if !ok || lane.Name() != "main" || worker.FacetLoader != nil {
					t.Fatalf("factory did not return the real main lane and absent loader: lane=%T loader=%v", worker.Lane, worker.FacetLoader)
				}
				model, err := lane.GetModel(t.Context())
				if err != nil || model == nil || model.ProviderMeta.ProviderID != "worker-factory" || model.ID != "model" || string(model.ProviderMeta.API) != api || model.ProviderMeta.BaseURL != "https://worker-factory.invalid/v1" || model.DisplayName != "Factory model" || model.Capabilities.ContextWindow != 32000 || model.Capabilities.MaxOutputTokens != 1234 {
					t.Fatalf("factory model=%+v error=%v", model, err)
				}
				thinking, err := lane.GetThinkingLevel(t.Context())
				if err != nil || thinking != selection.thinking {
					t.Fatalf("thinking=%q error=%v, want %q", thinking, err, selection.thinking)
				}
				wantTools := []string{"read", "write", "bash"}
				active, err := lane.GetActiveTools(t.Context())
				if err != nil || !reflect.DeepEqual(active, wantTools) {
					t.Fatalf("tools=%v error=%v, want %v", active, err, wantTools)
				}
				if err := lane.SetActiveTools(t.Context(), []string{"read"}); err != nil {
					t.Fatal(err)
				}
				metadata := stored.Metadata()
				if err := worker.Harness.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				reopened, err := repo.Open(t.Context(), metadata)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := reopened.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				next, err := createCodingAgentHarness(t.Context(), reopened, options, executionEnv)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := next.Harness.Close(context.Background()); err != nil {
						t.Error(err)
					}
				})
				active, err = next.Lane.(codingWorkerLane).GetActiveTools(t.Context())
				if err != nil || !reflect.DeepEqual(active, wantTools) {
					t.Fatalf("restored worker did not reset tool loadout: %v, %v", active, err)
				}
			})
		}
	}
}
