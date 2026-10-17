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

func TestPortWave12FireworksModelGeneration(t *testing.T) {
	isolateGeneratorEnvironment(t)
	// upstream: packages/ai/test/fireworks-model-generation.test.ts:67
	t.Run("combines upstream effort and toggle metadata with narrow corrections", func(t *testing.T) {
		models := generateFireworksFixture(t, map[string][]modelsDevReasoningOption{
			"deepseek-v4-flash-0731":       {{Type: "toggle"}, effortOptions("low", "high", "max")},
			"deepseek-v4-flash-vision-exp": {{Type: "toggle"}, effortOptions("low", "high", "max")},
			"deepseek-v4-pro-0813":         {{Type: "toggle"}, effortOptions("high", "max")},
			"qwen3p8-max":                  {{Type: "toggle"}},
			"qwen3p8-2p4t-a95b":            {effortOptions("low", "medium", "xhigh")},
			"kimi-k2p6":                    {{Type: "toggle"}},
		})
		for _, id := range []string{"deepseek-v4-flash-0731", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro-0813"} {
			assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/"+id], "thinkingLevelMap"), `{"off":"none","minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`)
		}
		for _, id := range []string{"qwen3p8-max", "qwen3p8-2p4t-a95b"} {
			assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/"+id], "thinkingLevelMap"), `{"off":"none","minimal":null,"low":"low","medium":"medium","high":null,"xhigh":"xhigh","max":null}`)
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

	// upstream: packages/ai/test/fireworks-model-generation.test.ts:107
	t.Run("automatically sends native effort for newly cataloged Messages models", func(t *testing.T) {
		models := generateFireworksFixture(t, map[string][]modelsDevReasoningOption{"new-reasoner": {{Type: "toggle"}, effortOptions("low", "max")}})
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

	// upstream: packages/ai/test/fireworks-model-generation.test.ts:128
	t.Run("does not infer adaptive thinking from toggle, budget, or missing metadata", func(t *testing.T) {
		models := generateFireworksFixture(t, map[string][]modelsDevReasoningOption{
			"toggle-only": {{Type: "toggle"}}, "budget-only": {{Type: "budget_tokens", Min: new(float64(1024))}}, "fixed-reasoning": {}, "missing-metadata": nil,
		})
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

	// upstream: packages/ai/test/fireworks-model-generation.test.ts:143
	t.Run("prefers updated upstream efforts over fixed maps and the Qwen fallback", func(t *testing.T) {
		models := generateFireworksFixture(t, map[string][]modelsDevReasoningOption{
			"deepseek-v4-flash-0731": {effortOptions("high", "max")}, "qwen3p8-max": {{Type: "toggle"}, effortOptions("medium", "xhigh")},
		})
		assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/deepseek-v4-flash-0731"], "thinkingLevelMap"), `{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":"max"}`)
		assertGeneratedJSON(t, generatedModelField(t, models["accounts/fireworks/models/qwen3p8-max"], "thinkingLevelMap"), `{"off":"none","minimal":null,"low":null,"medium":"medium","high":null,"xhigh":"xhigh","max":null}`)
	})
}
