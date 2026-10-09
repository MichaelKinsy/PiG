//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"testing"
	"time"

	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestPortWave13BedrockThinkingPayload(t *testing.T) {
	const profile = "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile"
	for _, tc := range []struct {
		name, base, id, displayName, region, thinking, effort, beta string
		level                                                       ModelThinkingLevel
		budgetAny                                                   bool
	}{
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:68
		{name: "uses adaptive thinking for Claude Opus 4.8 when reasoning is enabled", base: "global.anthropic.claude-opus-4-6-v1", id: "global.anthropic.claude-opus-4-8-v1", displayName: "Claude Opus 4.8 (Global)", thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "high", beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:83
		{name: "maps xhigh reasoning to effort=xhigh for Claude Opus 4.8", base: "global.anthropic.claude-opus-4-6-v1", id: "global.anthropic.claude-opus-4-8-v1", displayName: "Claude Opus 4.8 (Global)", level: ThinkingXHigh, thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "xhigh", beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:98
		{name: "uses adaptive thinking for Claude Fable 5 when reasoning is enabled", base: "global.anthropic.claude-fable-5", thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "high", beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:108
		{name: "uses adaptive thinking for Claude Sonnet 5 when reasoning is enabled", base: "global.anthropic.claude-sonnet-5", thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "high", beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:118
		{name: "uses adaptive thinking for Claude Opus 5 when reasoning is enabled", base: "global.anthropic.claude-opus-5", thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "high", beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:128
		{name: "maps xhigh reasoning to effort=xhigh for Claude Opus 5", base: "global.anthropic.claude-opus-5", level: ThinkingXHigh, thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "xhigh", beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:138
		{name: "maps xhigh reasoning to effort=xhigh for Claude Fable 5", base: "global.anthropic.claude-fable-5", level: ThinkingXHigh, thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, effort: "xhigh"},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:148 (#10324)
		{name: "sends block_binding and the binding beta for Claude Opus 5.5", base: "global.anthropic.claude-opus-5-5", thinking: `{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}}`, beta: `["thinking-binding-controls-2026-08-01"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:158 (Bedrock rejects block_binding on 4.6 models)
		{name: "omits block_binding for global.anthropic.claude-opus-4-6-v1", base: "global.anthropic.claude-opus-4-6-v1", thinking: `{"type":"adaptive","display":"summarized"}`, beta: "absent"},
		{name: "omits block_binding for global.anthropic.claude-sonnet-4-6", base: "global.anthropic.claude-sonnet-4-6", thinking: `{"type":"adaptive","display":"summarized"}`, beta: "absent"},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:170
		{name: "omits display for GovCloud model ids on non-adaptive Claude thinking", base: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", id: "us-gov.anthropic.claude-sonnet-4-5-20250929-v1:0", displayName: "Claude Sonnet 4.5 (GovCloud)", thinking: `{"type":"enabled","budget_tokens":16384}`, beta: `["interleaved-thinking-2025-05-14"]`},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:184
		{name: "omits display for GovCloud regions on adaptive Claude thinking", base: "global.anthropic.claude-opus-4-6-v1", id: "global.anthropic.claude-opus-4-8-v1", displayName: "Claude Opus 4.8 (Global)", region: "us-gov-west-1", thinking: `{"type":"adaptive"}`, effort: "high", beta: "absent"},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:234
		{name: "uses adaptive thinking when model.name contains the model name but ARN does not", base: "global.anthropic.claude-opus-4-6-v1", id: profile, displayName: "Claude Opus 4.6", thinking: `{"type":"adaptive","display":"summarized"}`, effort: "high"},
		// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:285
		{name: "falls back to fixed-budget thinking for non-adaptive Claude via model.name", base: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", id: profile, displayName: "Claude Sonnet 4.5", budgetAny: true, beta: `["interleaved-thinking-2025-05-14"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := cloneGeneratedModel(t, "amazon-bedrock/"+tc.base).ToModel()
			if tc.id != "" {
				model.ID = tc.id
				model.DisplayName = tc.displayName
			}
			level := tc.level
			if level == "" {
				level = ThinkingHigh
			}
			input := captureBedrockCommand(t, model, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: time.Now().UnixMilli()}}}, StreamOptions{Thinking: level.ReasoningOption(), IsReasoning: model.ProviderMeta.Reasoning, Region: tc.region})
			if input.AdditionalModelRequestFields == nil {
				t.Fatal("missing additionalModelRequestFields")
			}
			data, err := input.AdditionalModelRequestFields.MarshalSmithyDocument()
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if tc.budgetAny {
				var thinking struct {
					Type   string   `json:"type"`
					Budget *float64 `json:"budget_tokens"`
				}
				if err := json.Unmarshal(fields["thinking"], &thinking); err != nil {
					t.Fatal(err)
				}
				if thinking.Type != "enabled" || thinking.Budget == nil {
					t.Fatalf("thinking=%s; want enabled with numeric budget_tokens", fields["thinking"])
				}
			} else {
				assertShapeJSON(t, fields["thinking"], tc.thinking)
			}
			if tc.effort != "" {
				assertShapeJSON(t, fields["output_config"], `{"effort":"`+tc.effort+`"}`)
			}
			switch tc.beta {
			case "":
			case "absent":
				if value, present := fields["anthropic_beta"]; present {
					t.Fatalf("anthropic_beta=%s; want absent", value)
				}
			default:
				assertShapeJSON(t, fields["anthropic_beta"], tc.beta)
			}
		})
	}
	// upstream: packages/ai/test/bedrock-thinking-payload.test.ts:248
	t.Run("injects cache points when model.name identifies a supported Claude model", func(t *testing.T) {
		model := cloneGeneratedModel(t, "amazon-bedrock/global.anthropic.claude-opus-4-6-v1").ToModel()
		model.ID, model.DisplayName = profile, "Claude Sonnet 4.6"
		input := captureBedrockCommand(t, model, Context{SystemPrompt: "You are helpful.", Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: time.Now().UnixMilli()}}}, StreamOptions{})
		if len(input.System) != 2 {
			t.Fatalf("system blocks=%d; want 2", len(input.System))
		}
		if _, ok := input.System[1].(*btypes.SystemContentBlockMemberCachePoint); !ok {
			t.Fatalf("system[1]=%T; want cachePoint", input.System[1])
		}
		if len(input.Messages) == 0 {
			t.Fatal("missing messages")
		}
		content := input.Messages[len(input.Messages)-1].Content
		if len(content) == 0 {
			t.Fatal("missing last message content")
		}
		if _, ok := content[len(content)-1].(*btypes.ContentBlockMemberCachePoint); !ok {
			t.Fatalf("last content=%T; want cachePoint", content[len(content)-1])
		}
	})
}
