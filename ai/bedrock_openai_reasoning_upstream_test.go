//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"testing"
	"time"
)

// Pi 1.1.0 additions to packages/ai/test/bedrock-thinking-payload.test.ts ("Bedrock OpenAI reasoning payload", :201-262) and bedrock-converse-stream.ts:776-806,883-892 (haiku-5 joins the adaptive, xhigh, block-binding and cache families).

func bedrockFieldsFor(t *testing.T, model *Model, options StreamOptions) map[string]json.RawMessage {
	t.Helper()
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

// bedrockReasoningModel clones a reasoning Bedrock catalog model and gives it another id and name, as the upstream tests do for application inference profiles; the catalog of this checkout does not list the OpenAI and Haiku 5 Bedrock entries yet.
func bedrockReasoningModel(t *testing.T, id, name string) *Model {
	t.Helper()
	model := cloneGeneratedModel(t, "amazon-bedrock/global.anthropic.claude-opus-4-6-v1").ToModel()
	model.ID, model.DisplayName = id, name
	model.ThinkingLevelMap = nil
	return model
}

// upstream: bedrock-thinking-payload.test.ts:212 (#9331): the configured thinking level reaches OpenAI GPT models on Bedrock as reasoning.effort; minimal is sent as low.
func TestBedrockSendsReasoningEffortForOpenAIGPTModels(t *testing.T) {
	for _, tc := range []struct {
		level  ModelThinkingLevel
		effort string
	}{{ThinkingMinimal, "low"}, {ThinkingLow, "low"}, {ThinkingMedium, "medium"}, {ThinkingHigh, "high"}, {ThinkingXHigh, "xhigh"}, {ThinkingMax, "max"}} {
		for _, id := range []string{"global.openai.gpt-6-sol", "us.openai.gpt-6-luna", "global.openai.gpt-5.6-sol"} {
			model := bedrockReasoningModel(t, id, "")
			fields := bedrockFieldsFor(t, model, StreamOptions{Thinking: tc.level.ReasoningOption(), IsReasoning: true})
			assertShapeJSON(t, mustJSON(t, fields), `{"reasoning":{"effort":"`+tc.effort+`"}}`)
		}
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// upstream: bedrock-thinking-payload.test.ts:224 "sends reasoning.effort when only model.name identifies a GPT model"
func TestBedrockSendsReasoningEffortWhenOnlyTheModelNameIdentifiesAGPTModel(t *testing.T) {
	model := bedrockReasoningModel(t, "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile", "GPT-6 Sol")
	fields := bedrockFieldsFor(t, model, StreamOptions{Thinking: ThinkingMedium.ReasoningOption(), IsReasoning: true})
	assertShapeJSON(t, mustJSON(t, fields), `{"reasoning":{"effort":"medium"}}`)
}

// A model's own thinkingLevelMap entry wins over the default mapping (bedrock-converse-stream.ts:1326-1328).
func TestBedrockOpenAIReasoningEffortHonorsTheModelsThinkingLevelMap(t *testing.T) {
	model := bedrockReasoningModel(t, "global.openai.gpt-6-sol", "")
	model.ThinkingLevelMap = ThinkingLevelMap{ThinkingMedium: new("high")}
	fields := bedrockFieldsFor(t, model, StreamOptions{Thinking: ThinkingMedium.ReasoningOption(), IsReasoning: true})
	assertShapeJSON(t, mustJSON(t, fields), `{"reasoning":{"effort":"high"}}`)
}

// upstream: bedrock-thinking-payload.test.ts:238 "sends flat reasoning_effort for gpt-oss, clamped to high"
func TestBedrockSendsFlatReasoningEffortForGPTOSSClampedToHigh(t *testing.T) {
	model := bedrockReasoningModel(t, "openai.gpt-oss-120b-1:0", "")
	for level, effort := range map[ModelThinkingLevel]string{ThinkingMinimal: "low", ThinkingMedium: "medium", ThinkingXHigh: "high", ThinkingMax: "high"} {
		fields := bedrockFieldsFor(t, model, StreamOptions{Thinking: level.ReasoningOption(), IsReasoning: true})
		assertShapeJSON(t, mustJSON(t, fields), `{"reasoning_effort":"`+effort+`"}`)
	}
}

// upstream: bedrock-thinking-payload.test.ts:252 "sends no reasoning fields when reasoning is off"; a model that is neither Claude nor GPT gets none either.
func TestBedrockSendsNoReasoningFieldsWhenReasoningIsOffOrTheModelIsUnknown(t *testing.T) {
	if fields := bedrockFieldsFor(t, bedrockReasoningModel(t, "global.openai.gpt-6-sol", ""), StreamOptions{}); fields != nil {
		t.Fatalf("reasoning off sent %v", fields)
	}
	if fields := bedrockFieldsFor(t, bedrockReasoningModel(t, "meta.llama4-maverick", ""), StreamOptions{Thinking: ThinkingHigh.ReasoningOption(), IsReasoning: true}); fields != nil {
		t.Fatalf("an unrelated model sent %v", fields)
	}
}

// bedrock-converse-stream.ts:776-806: Claude Haiku 5.x takes adaptive thinking, native xhigh effort and block binding like the other Claude 5 models.
func TestBedrockClaudeHaiku5UsesAdaptiveThinkingWithNativeXhighAndBlockBinding(t *testing.T) {
	model := bedrockReasoningModel(t, "global.anthropic.claude-haiku-5-5", "Claude Haiku 5.5 (Global)")
	fields := bedrockFieldsFor(t, model, StreamOptions{Thinking: ThinkingXHigh.ReasoningOption(), IsReasoning: true})
	assertShapeJSON(t, fields["thinking"], `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`)
	assertShapeJSON(t, fields["output_config"], `{"effort":"xhigh"}`)
	assertShapeJSON(t, fields["anthropic_beta"], `["thinking-binding-controls-2026-08-01"]`)
}

// bedrock-converse-stream.ts:883-892: Claude 5 models, haiku-5 included, support prompt caching whatever their id looks like.
func TestSupportsBedrockPromptCachingForClaudeHaiku5(t *testing.T) {
	for _, id := range []string{"global.anthropic.claude-haiku-5-5", "claude-haiku-5"} {
		if !supportsBedrockPromptCaching(id, "", nil) {
			t.Errorf("supportsBedrockPromptCaching(%q) = false, want true", id)
		}
	}
}
