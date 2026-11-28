//go:build !pig_strip_bedrock_converse_stream

package bedrock

// Ports Pi v1.7.0 Bedrock tests from:
// - packages/ai/test/bedrock-convert-messages.test.ts (Bedrock reasoning replay, #12555)
// - packages/ai/test/bedrock-thinking-payload.test.ts (Bedrock flat reasoning_effort payload, #12551)

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestBedrockReasoningReplay(t *testing.T) {
	toolTurn := func(modelID string) []ai.Message {
		return []ai.Message{
			ai.UserMessage{Content: ai.UserText("Read the file")},
			ai.AssistantMessage{
				Content: []ai.AssistantContentBlock{
					ai.ThinkingContent{Thinking: "I should read it."},
					ai.ToolCall{ID: "tool-1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
				},
				API:        ai.APIBedrockConverseStream,
				Provider:   "amazon-bedrock",
				Model:      modelID,
				StopReason: ai.StopReasonToolUse,
			},
			ai.ToolResultMessage{
				ToolCallID: "tool-1",
				ToolName:   "read",
				Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: "hello"}},
			},
		}
	}

	t.Run("sends earlier reasoning back by default", func(t *testing.T) {
		msgs, err := convertBedrockMessages(toolTurn("amazon.nova-pro-v1:0"), "amazon.nova-pro-v1:0", "Nova Pro", bedrockCacheNone, ai.ProviderEnv{})
		if err != nil {
			t.Fatalf("convertBedrockMessages: %v", err)
		}
		if len(msgs) < 2 || len(msgs[1].Content) != 2 {
			t.Fatalf("expected 2 assistant content blocks (reasoning + toolUse), got %#v", msgs)
		}
	})

	t.Run("drops earlier reasoning when supportsReasoningReplay is false", func(t *testing.T) {
		for _, id := range []string{"global.moonshotai.kimi-k3", "us.moonshotai.kimi-k3.1"} {
			msgs, err := convertBedrockMessages(toolTurn(id), id, "Kimi K3", bedrockCacheNone, ai.ProviderEnv{}, false)
			if err != nil {
				t.Fatalf("%s: convertBedrockMessages: %v", id, err)
			}
			if len(msgs) < 2 || len(msgs[1].Content) != 1 {
				t.Fatalf("%s: expected 1 assistant content block (toolUse only), got %#v", id, msgs[1].Content)
			}
		}
	})
}

func TestBedrockFlatReasoningEffortPayload(t *testing.T) {
	strPtr := func(s string) *string { return &s }
	kimiModel := &ai.Model{
		ID:          "global.moonshotai.kimi-k3.1",
		DisplayName: "Kimi K3.1",
		ProviderMeta: ai.ProviderMetadata{
			ProviderID: "amazon-bedrock",
			API:        ai.APIBedrockConverseStream,
			Reasoning:  true,
		},
		ThinkingLevelMap: ai.ThinkingLevelMap{
			ai.ThinkingOff:    strPtr("none"),
			ai.ThinkingLow:    strPtr("low"),
			ai.ThinkingMedium: strPtr("medium"),
			ai.ThinkingHigh:   strPtr("high"),
			ai.ThinkingXHigh:  strPtr("xhigh"),
			ai.ThinkingMax:    strPtr("max"),
		},
	}
	glmModel := &ai.Model{
		ID:          "global.zai.glm-5.3",
		DisplayName: "GLM 5.3",
		ProviderMeta: ai.ProviderMetadata{
			ProviderID: "amazon-bedrock",
			API:        ai.APIBedrockConverseStream,
			Reasoning:  true,
		},
		ThinkingLevelMap: ai.ThinkingLevelMap{
			ai.ThinkingOff:    nil,
			ai.ThinkingLow:    strPtr("low"),
			ai.ThinkingMedium: strPtr("medium"),
			ai.ThinkingHigh:   strPtr("high"),
			ai.ThinkingXHigh:  strPtr("xhigh"),
			ai.ThinkingMax:    strPtr("max"),
		},
	}

	for _, level := range []ai.ThinkingLevel{ai.ThinkingLevelLow, ai.ThinkingLevelMedium, ai.ThinkingLevelHigh, ai.ThinkingLevelXHigh, ai.ThinkingLevelMax} {
		for _, m := range []*ai.Model{kimiModel, glmModel} {
			got := buildBedrockAdditionalFieldsWithBudget(m, m.DisplayName, ai.StreamOptions{IsReasoning: true, Thinking: level}, nil)
			want := map[string]any{"reasoning_effort": string(level)}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s level=%s: got %#v, want %#v", m.ID, level, got, want)
			}
		}
	}

	t.Run("sends reasoning_effort=none when reasoning is off and model maps off", func(t *testing.T) {
		got := buildBedrockAdditionalFieldsWithBudget(kimiModel, kimiModel.DisplayName, ai.StreamOptions{IsReasoning: true}, nil)
		want := map[string]any{"reasoning_effort": "none"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("sends no reasoning_effort when reasoning is off and model hides off", func(t *testing.T) {
		got := buildBedrockAdditionalFieldsWithBudget(glmModel, glmModel.DisplayName, ai.StreamOptions{IsReasoning: true}, nil)
		if got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})

	t.Run("sends no reasoning_effort when model lists no effort values", func(t *testing.T) {
		noMap := *kimiModel
		noMap.ThinkingLevelMap = nil
		got := buildBedrockAdditionalFieldsWithBudget(&noMap, noMap.DisplayName, ai.StreamOptions{IsReasoning: true, Thinking: ai.ThinkingLevelMedium}, nil)
		if got != nil {
			t.Fatalf("got %#v, want nil", got)
		}
	})
}
