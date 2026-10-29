package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// reasoningOption is a ModelsDevReasoningOption of the upstream test: toggle, effort (values) or budget_tokens (min).
type reasoningOption struct {
	kind   string
	values []string
	min    *float64
}

func toggleOption() reasoningOption { return reasoningOption{kind: "toggle"} }

func effortOption(values ...string) reasoningOption {
	return reasoningOption{kind: "effort", values: values}
}

// generateFireworksModels ports generateFireworksModels of fireworks-model-generation.test.ts. A nil option list is a model without
// reasoning metadata, an empty one a model with fixed reasoning.
func generateFireworksModels(t *testing.T, options map[string][]reasoningOption) map[string]json.RawMessage {
	t.Helper()
	root := t.TempDir()
	var models []map[string]any
	for id, reasoningOptions := range options {
		reasoning := map[string]any{"supported": true}
		for _, option := range reasoningOptions {
			switch option.kind {
			case "toggle":
				reasoning["toggle"] = true
			case "effort":
				if _, found := reasoning["effort"]; !found {
					reasoning["effort"] = option.values
				}
			case "budget_tokens":
				if _, found := reasoning["budget"]; !found {
					reasoning["budget"] = map[string]any{"min": option.min}
				}
			}
		}
		model := sourceModel("accounts/fireworks/models/"+id, map[string]any{})
		model["capabilities"] = map[string]any{"tools": map[string]any{"supported": true}, "reasoning": reasoning}
		models = append(models, model)
	}
	output := filepath.Join(root, "catalog")
	status, stdout, stderr := runGeneratorSubprocess(t, root, providerCatalog("fireworks-ai", models...), "--json-only", "--json-output", output)
	if status != 0 {
		t.Fatalf("generator status = %d, want 0\n%s\n%s", status, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("generator stderr = %q, want empty", stderr)
	}
	return decodeJSONObject(t, filepath.Join(output, "providers", "fireworks.json"))
}

func requireMessagesModel(t *testing.T, raw json.RawMessage) {
	t.Helper()
	assertGeneratedJSON(t, generatedModelField(t, raw, "api"), `"anthropic-messages"`)
}

func TestFireworksModelGeneration(t *testing.T) {
	isolateGeneratorEnvironment(t)
	// Regression for #9323: import catalog efforts and correct only the known omissions.
	// upstream: packages/ai/test/fireworks-model-generation.test.ts:63
	t.Run("combines upstream effort and toggle metadata with narrow corrections", func(t *testing.T) {
		models := generateFireworksModels(t, map[string][]reasoningOption{
			"deepseek-v4-flash-0731":       {toggleOption(), effortOption("low", "high", "max")},
			"deepseek-v4-flash-vision-exp": {toggleOption(), effortOption("low", "high", "max")},
			"deepseek-v4-pro-0813":         {toggleOption(), effortOption("high", "max")},
			"qwen3p8-max":                  {toggleOption()},
			"qwen3p8-2p4t-a95b":            {effortOption("low", "medium", "xhigh")},
			"kimi-k2p6":                    {toggleOption()},
		})
		for _, id := range []string{"deepseek-v4-flash-0731", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro-0813"} {
			assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/"+id], "thinkingLevelMap"), `{"off":"none","minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`)
		}
		for _, id := range []string{"qwen3p8-max", "qwen3p8-2p4t-a95b"} {
			assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/"+id], "thinkingLevelMap"), `{"off":"none","minimal":null,"low":"low","medium":"medium","high":null,"xhigh":"xhigh","max":null}`)
		}
		if len(models) != 6 {
			t.Errorf("generated %d models, want 6", len(models))
		}
		for id, model := range models {
			requireMessagesModel(t, model)
			compat := generatedModelField(t, model, "compat")
			assertGeneratedJSON(t, generatedModelField(t, compat, "allowEmptySignature"), `true`)
			adaptive := generatedModelField(t, compat, "forceAdaptiveThinking")
			if id == "accounts/fireworks/models/kimi-k2p6" {
				if adaptive != nil {
					t.Errorf("%s forceAdaptiveThinking = %s, want absent", id, adaptive)
				}
			} else {
				assertGeneratedJSON(t, adaptive, `true`)
			}
		}
		if got := generatedModelField(t, models["accounts/fireworks/models/kimi-k2p6"], "thinkingLevelMap"); got != nil {
			t.Errorf("kimi thinkingLevelMap = %s, want absent", got)
		}
	})

	// Regression for #9323: new effort-capable models must not require an allowlist update.
	// upstream: packages/ai/test/fireworks-model-generation.test.ts:103
	t.Run("automatically sends native effort for newly cataloged Messages models", func(t *testing.T) {
		models := generateFireworksModels(t, map[string][]reasoningOption{"new-reasoner": {toggleOption(), effortOption("low", "max")}})
		raw := models["accounts/fireworks/models/new-reasoner"]
		requireMessagesModel(t, raw)
		assertGeneratedJSON(t, generatedModelField(t, generatedModelField(t, raw, "compat"), "forceAdaptiveThinking"), `true`)
		data, err := decodeJSONModel(raw)
		if err != nil {
			t.Fatal(err)
		}
		var compat *ai.ModelCompat
		if err := json.Unmarshal(generatedModelField(t, raw, "compat"), &compat); err != nil {
			t.Fatal(err)
		}
		thinking := make(map[ai.ModelThinkingLevel]*string)
		for level, value := range data.ThinkingLevelMap {
			thinking[ai.ModelThinkingLevel(level)] = value
		}
		model := (&ai.GeneratedModel{ID: data.ID, Provider: data.Provider, DisplayName: data.Name, API: ai.API(data.API), BaseURL: data.BaseURL, Reasoning: data.Reasoning,
			Capabilities: data.Input, ContextWindow: data.ContextWindow, MaxOutputTokens: data.MaxTokens, Compat: compat, ThinkingLevelMap: thinking,
			InputCostPerMTokens: data.Cost.Input, OutputCostPerMTokens: data.Cost.Output, CacheReadCost: data.Cost.CacheRead, CacheWriteCost: data.Cost.CacheWrite}).ToModel()
		if got := ai.GetSupportedThinkingLevels(model); !reflect.DeepEqual(got, []ai.ModelThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingMax}) {
			t.Errorf("thinking levels = %v, want [off low max]", got)
		}
		root := t.TempDir()
		services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: root, AgentDir: filepath.Join(root, "agent")})
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]json.RawMessage
		ctx := extension.WithModelStreamRequest(t.Context(), extension.ModelStreamRequest{API: true})
		result := services.ModelRuntime().CompleteSimple(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("test"), Timestamp: 0}}}, ai.StreamOptions{
			APIKey: "test-fireworks-key", Thinking: ai.ThinkingLevelMax,
			OnPayload: func(value any, _ *ai.Model) (any, error) {
				encoded, err := json.Marshal(value)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(encoded, &payload); err != nil {
					return nil, err
				}
				return nil, errors.New("payload captured")
			},
		})
		// Pi's onPayload rejection terminates the assistant result with the original error message.
		if payload == nil || result.StopReason != ai.StopReasonError || result.ErrorMessage != "payload captured" {
			t.Fatalf("payload capture did not terminate the request: %+v", result)
		}
		assertGeneratedJSON(t, payload["thinking"], `{"type":"adaptive","display":"summarized"}`)
		assertGeneratedJSON(t, payload["output_config"], `{"effort":"max"}`)
	})

	// upstream: packages/ai/test/fireworks-model-generation.test.ts:124
	t.Run("does not infer adaptive thinking from toggle, budget, or missing metadata", func(t *testing.T) {
		min := 1024.0
		models := generateFireworksModels(t, map[string][]reasoningOption{
			"toggle-only": {toggleOption()}, "budget-only": {{kind: "budget_tokens", min: &min}}, "fixed-reasoning": {}, "missing-metadata": nil,
		})
		if len(models) != 4 {
			t.Errorf("generated %d models, want 4", len(models))
		}
		for id, model := range models {
			requireMessagesModel(t, model)
			if got := generatedModelField(t, generatedModelField(t, model, "compat"), "forceAdaptiveThinking"); got != nil {
				t.Errorf("%s forceAdaptiveThinking = %s, want absent", id, got)
			}
			if got := generatedModelField(t, model, "thinkingLevelMap"); got != nil {
				t.Errorf("%s thinkingLevelMap = %s, want absent", id, got)
			}
		}
	})

	// Regression for #9323: full hardcoded maps must not override updated catalog efforts.
	// upstream: packages/ai/test/fireworks-model-generation.test.ts:139
	t.Run("prefers updated upstream efforts over fixed maps and the Qwen fallback", func(t *testing.T) {
		models := generateFireworksModels(t, map[string][]reasoningOption{
			"deepseek-v4-flash-0731": {effortOption("high", "max")}, "qwen3p8-max": {toggleOption(), effortOption("medium", "xhigh")},
		})
		assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/deepseek-v4-flash-0731"], "thinkingLevelMap"), `{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":"max"}`)
		assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/qwen3p8-max"], "thinkingLevelMap"), `{"off":"none","minimal":null,"low":null,"medium":"medium","high":null,"xhigh":"xhigh","max":null}`)
	})
}
