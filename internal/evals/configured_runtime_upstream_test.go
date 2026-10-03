package evals

// Ports packages/evals/test/configured-runtime.test.ts.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

var acmeModel = ModelFields{
	ID:            OpenAIModelID,
	Name:          "Acme Chat",
	Provider:      OpenAIProviderID,
	Reasoning:     false,
	Input:         []string{"text"},
	Cost:          ModelCostFields{},
	ContextWindow: 32768,
	MaxTokens:     4096,
}

// isolateAgentDir points the default agent directory, which a runtime without explicit paths reads, at an empty
// temporary directory.
func isolateAgentDir(t *testing.T) {
	t.Helper()
	t.Setenv(agentDirEnvironment(), t.TempDir())
}

func agentDirWith(t *testing.T, modelsJSON any) string {
	t.Helper()
	directory := t.TempDir()
	encoded, err := json.Marshal(modelsJSON)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "models.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func loadRuntime(t *testing.T, agentDir string) *coding.ModelRuntime {
	t.Helper()
	runtime, err := LoadConfiguredModelRuntime(t.Context(), agentDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	return runtime
}

func acmeModelsJSON(server *AcmeServer) map[string]any {
	return map[string]any{"providers": map[string]any{OpenAIProviderID: map[string]any{
		"baseUrl": server.BaseURL(),
		"api":     "openai-completions",
		"apiKey":  "$ACME_API_KEY",
		"models": []any{map[string]any{
			"id": acmeModel.ID, "name": acmeModel.Name, "reasoning": acmeModel.Reasoning, "input": acmeModel.Input,
			"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
			"contextWindow": acmeModel.ContextWindow, "maxTokens": acmeModel.MaxTokens,
		}},
	}}}
}

func probe(server *AcmeServer, content, apiKey string) ProviderScenario {
	return ProviderScenario{
		ProviderID: OpenAIProviderID,
		ModelID:    OpenAIModelID,
		CreateContext: func() ai.Context {
			return ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(content)}}}
		},
		Options:              ai.StreamOptions{Env: ai.ProviderEnv{"ACME_API_KEY": apiKey}, MaxTokens: 32},
		ValidRequestReceived: server.ValidRequestReceived,
	}
}

func probeOutput(valid bool, text, stopReason string, inputTokens, outputTokens int) ProviderRuntimeOutput {
	return ProviderRuntimeOutput{Result: ProviderRuntimeResult{ProviderProbe: &ProviderProbe{
		ValidRequestReceived: valid,
		Model:                acmeModel,
		Response:             ProviderProbeResponse{Text: text, StopReason: stopReason, InputTokens: inputTokens, OutputTokens: outputTokens},
	}}}
}

func requireOutput[T any](t *testing.T, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("output:\n got %s\nwant %s", gotJSON, wantJSON)
	}
}

// TestConfiguredRuntimeUpstream ports packages/evals/test/configured-runtime.test.ts. Each subtest names one upstream
// case.
func TestConfiguredRuntimeUpstream(t *testing.T) {
	isolateAgentDir(t)
	server := CreateAcmeServer(AcmeServerModeOpenAI)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})

	t.Run("inspectProvider › probes a models.json provider through ModelRuntime", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:82
		server.Reset()
		runtime := loadRuntime(t, agentDirWith(t, acmeModelsJSON(server)))
		requireOutput(t, InspectProvider(t.Context(), runtime, probe(server, OpenAIProbePrompt, "resolved-acme-key")),
			probeOutput(true, OpenAIProbeResponse, "stop", 3, 2))
	})

	t.Run("inspectProvider › keeps a non-probe completion distinct from a valid probe", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:93
		server.Reset()
		runtime := loadRuntime(t, agentDirWith(t, acmeModelsJSON(server)))
		requireOutput(t, InspectProvider(t.Context(), runtime, probe(server, "hello", "resolved-acme-key")),
			probeOutput(false, OpenAIProbeResponse, "stop", 3, 2))
	})

	t.Run("inspectProvider › returns a structured error when the configured model is missing", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:104
		server.Reset()
		runtime := loadRuntime(t, agentDirWith(t, map[string]any{"providers": map[string]any{}}))
		requireOutput(t, InspectProvider(t.Context(), runtime, probe(server, OpenAIProbePrompt, "resolved-acme-key")),
			ProviderRuntimeOutput{Result: ProviderRuntimeResult{Error: "Model " + OpenAIProviderID + "/" + OpenAIModelID + " is unavailable after reload."}})
		if server.ValidRequestReceived() {
			t.Fatal("a missing model reached the server")
		}
	})

	t.Run("inspectProvider › returns a structured error when models.json cannot be parsed", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:112
		server.Reset()
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "models.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		output := InspectProvider(t.Context(), loadRuntime(t, directory), probe(server, OpenAIProbePrompt, "resolved-acme-key"))
		if output.Result.ProviderProbe != nil || !strings.Contains(output.Result.Error, "Failed to parse models.json") {
			t.Fatalf("output = %+v", output)
		}
		if server.ValidRequestReceived() {
			t.Fatal("an unparsable configuration reached the server")
		}
	})

	t.Run("inspectProvider › does not treat an unauthorized completion as a valid probe", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:126
		server.Reset()
		runtime := loadRuntime(t, agentDirWith(t, acmeModelsJSON(server)))
		requireOutput(t, InspectProvider(t.Context(), runtime, probe(server, OpenAIProbePrompt, "wrong-key")),
			probeOutput(false, "", "error", 0, 0))
	})

	const addedProvider = "openai"
	const addedModelID = "fixture-chat"
	addedModel := ModelFields{
		ID: addedModelID, Name: "Fixture Chat", Provider: addedProvider, Reasoning: true, Input: []string{"text"},
		ContextWindow: 32768, MaxTokens: 4096,
	}

	t.Run("inspectAddedModel › loads an added model without dropping built-in models", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:152
		directory := agentDirWith(t, map[string]any{"providers": map[string]any{addedProvider: map[string]any{
			"models": []any{map[string]any{
				"id": addedModel.ID, "name": addedModel.Name, "reasoning": addedModel.Reasoning, "input": addedModel.Input,
				"cost":          map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0},
				"contextWindow": addedModel.ContextWindow, "maxTokens": addedModel.MaxTokens,
			}},
		}}})
		output, err := InspectAddedModel(t.Context(), loadRuntime(t, directory), addedProvider, addedModelID)
		if err != nil {
			t.Fatal(err)
		}
		requireOutput(t, output, AddedModelOutput{Result: AddedModelResult{AddedModel: &AddedModel{Model: addedModel, ExistingModelsPreserved: true}}})
	})

	t.Run("inspectAddedModel › returns a structured error when the added model is missing", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:177
		directory := agentDirWith(t, map[string]any{"providers": map[string]any{}})
		output, err := InspectAddedModel(t.Context(), loadRuntime(t, directory), addedProvider, addedModelID)
		if err != nil {
			t.Fatal(err)
		}
		requireOutput(t, output, AddedModelOutput{Result: AddedModelResult{Error: "Model " + addedProvider + "/" + addedModelID + " is unavailable after reload."}})
	})

	t.Run("inspectAddedModel › returns a structured error when models.json cannot be parsed", func(t *testing.T) {
		// upstream: packages/evals/test/configured-runtime.test.ts:186
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "models.json"), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		output, err := InspectAddedModel(t.Context(), loadRuntime(t, directory), addedProvider, addedModelID)
		if err != nil {
			t.Fatal(err)
		}
		if output.Result.AddedModel != nil || !strings.Contains(output.Result.Error, "Failed to parse models.json") {
			t.Fatalf("output = %+v", output)
		}
	})
}
