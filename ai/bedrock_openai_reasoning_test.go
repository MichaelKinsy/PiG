//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"testing"
	"time"
)

func bedrockAdditionalFields(t *testing.T, model *Model, level ModelThinkingLevel) map[string]json.RawMessage {
	t.Helper()
	options := StreamOptions{IsReasoning: model.ProviderMeta.Reasoning}
	if level != "" {
		options.Thinking = level.ReasoningOption()
	}
	input := captureBedrockCommand(t, model, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: time.Now().UnixMilli()}}}, options)
	if input.AdditionalModelRequestFields == nil {
		return nil
	}
	data, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

// Ports the "Bedrock OpenAI reasoning payload" cases of packages/ai/test/bedrock-thinking-payload.test.ts. Regression for #9331: the configured thinking level never reached OpenAI models on Bedrock.
func TestBedrockOpenAIReasoningPayload(t *testing.T) {
	for _, tc := range []struct {
		reasoning ModelThinkingLevel
		effort    string
	}{{ThinkingMinimal, "low"}, {ThinkingLow, "low"}, {ThinkingMedium, "medium"}, {ThinkingHigh, "high"}, {ThinkingXHigh, "xhigh"}, {ThinkingMax, "max"}} {
		t.Run("sends reasoning="+string(tc.reasoning)+" as reasoning.effort="+tc.effort+" for GPT-6 and GPT-5.6", func(t *testing.T) {
			for _, id := range []string{"global.openai.gpt-6-sol", "us.openai.gpt-6-luna", "global.openai.gpt-5.6-sol"} {
				fields := bedrockAdditionalFields(t, cloneGeneratedModel(t, "amazon-bedrock/"+id).ToModel(), tc.reasoning)
				if len(fields) != 1 {
					t.Fatalf("%s: additionalModelRequestFields = %v, want only reasoning", id, fields)
				}
				assertShapeJSON(t, fields["reasoning"], `{"effort":"`+tc.effort+`"}`)
			}
		})
	}
	t.Run("sends reasoning.effort when only model.name identifies a GPT model", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/global.openai.gpt-6-sol").ToModel()
		model.ID = "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile"
		model.DisplayName = "GPT-6 Sol"
		fields := bedrockAdditionalFields(t, model, ThinkingMedium)
		if len(fields) != 1 {
			t.Fatalf("fields = %v", fields)
		}
		assertShapeJSON(t, fields["reasoning"], `{"effort":"medium"}`)
	})
	t.Run("a thinking level map entry names the effort", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/global.openai.gpt-6-sol").ToModel()
		model.ThinkingLevelMap = ThinkingLevelMap{ThinkingMedium: new("custom")}
		assertShapeJSON(t, bedrockAdditionalFields(t, model, ThinkingMedium)["reasoning"], `{"effort":"custom"}`)
	})
	t.Run("without a thinking level map the effort follows OPENAI_GPT_EFFORT", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/global.openai.gpt-6-sol").ToModel()
		model.ThinkingLevelMap = nil
		for level, want := range map[ModelThinkingLevel]string{ThinkingMinimal: "low", ThinkingLow: "low", ThinkingMedium: "medium", ThinkingHigh: "high", ThinkingXHigh: "xhigh", ThinkingMax: "max"} {
			assertShapeJSON(t, bedrockAdditionalFields(t, model, level)["reasoning"], `{"effort":"`+want+`"}`)
		}
	})
	t.Run("sends flat reasoning_effort for gpt-oss, clamped to high", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/openai.gpt-oss-120b-1:0").ToModel()
		for level, want := range map[ModelThinkingLevel]string{ThinkingMinimal: "low", ThinkingMedium: "medium", ThinkingXHigh: "high"} {
			fields := bedrockAdditionalFields(t, model, level)
			if len(fields) != 1 {
				t.Fatalf("%s: fields = %v", level, fields)
			}
			assertShapeJSON(t, fields["reasoning_effort"], `"`+want+`"`)
		}
	})
	t.Run("sends no reasoning fields when reasoning is off", func(t *testing.T) {
		if fields := bedrockAdditionalFields(t, cloneGeneratedModel(t, "amazon-bedrock/global.openai.gpt-6-sol").ToModel(), ""); fields != nil {
			t.Fatalf("fields = %v, want none", fields)
		}
	})
	// bedrock-converse-stream.ts: haiku-5 joins the Claude 5 families for adaptive thinking, block binding, native xhigh and cache points.
	t.Run("Claude Haiku 5 thinks adaptively with block binding", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/global.anthropic.claude-opus-5").ToModel()
		model.ID, model.DisplayName = "global.anthropic.claude-haiku-5-5", "Claude Haiku 5.5"
		fields := bedrockAdditionalFields(t, model, ThinkingXHigh)
		assertShapeJSON(t, fields["thinking"], `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`)
		assertShapeJSON(t, fields["output_config"], `{"effort":"xhigh"}`)
		assertShapeJSON(t, fields["anthropic_beta"], `["thinking-binding-controls-2026-08-01"]`)
	})
}
