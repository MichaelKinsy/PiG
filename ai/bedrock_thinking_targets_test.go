//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"encoding/json"
	"testing"
)

// upstream: packages/ai/src/api/bedrock-converse-stream.ts:763-810,854-873.
func TestBedrockOpus5Capabilities(t *testing.T) {
	t.Parallel()
	for _, model := range []*Model{
		{ID: "global.anthropic.claude-opus-5"},
		{ID: "arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/my-profile", DisplayName: "Claude Opus 5"},
	} {
		t.Run(model.ID, func(t *testing.T) {
			t.Parallel()
			if !supportsBedrockAdaptiveThinkingWithName(model.ID, model.DisplayName) || !supportsNativeXhighEffort(model) || !supportsBedrockPromptCaching(model.ID, model.DisplayName, nil) {
				t.Fatal("Opus 5 must support adaptive thinking, native xhigh, and prompt caching")
			}
			// Native xhigh takes precedence over the inherited Opus 4.6 level map.
			model.ThinkingLevelMap = ThinkingLevelMap{ThinkingXHigh: new("max")}
			if got := mapBedrockThinkingEffort(model, ThinkingXHigh); got != "xhigh" {
				t.Fatalf("effort = %q, want xhigh", got)
			}
		})
	}
}

// upstream: packages/ai/src/api/bedrock-converse-stream.ts:1175-1182,1259-1280. GovCloud omits both display and the block_binding/beta pair.
func TestBedrockGovCloudThinkingDisplay(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name, id, region string
		env              ProviderEnv
		display          bool
	}{
		{"commercial", "global.anthropic.claude-opus-5", "us-east-1", nil, true},
		{"model", "US-GOV.anthropic.claude-opus-5", "us-east-1", nil, false},
		{"arn", "arn:aws-us-gov:bedrock:us-gov-west-1:123:application-inference-profile/my-profile", "us-east-1", nil, false},
		{"option", "global.anthropic.claude-opus-5", "US-GOV-WEST-1", nil, false},
		{"scoped region", "global.anthropic.claude-opus-5", "", ProviderEnv{"AWS_REGION": "us-gov-west-1", "AWS_DEFAULT_REGION": "us-east-1"}, false},
		{"scoped default", "global.anthropic.claude-opus-5", "", ProviderEnv{"AWS_REGION": "", "AWS_DEFAULT_REGION": "us-gov-east-1"}, false},
		{"explicit precedence", "global.anthropic.claude-opus-5", "us-east-1", ProviderEnv{"AWS_REGION": "us-gov-west-1"}, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			fields := buildBedrockAdditionalFields(&Model{ID: row.id, DisplayName: "Claude Opus 5"}, "Claude Opus 5", StreamOptions{Thinking: ThinkingLevelHigh, IsReasoning: true, Region: row.region, Env: row.env})
			data, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			want := `{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}`
			if row.display {
				want = `{"thinking":{"type":"adaptive","display":"summarized","block_binding":{"prefix_mismatch_behavior":"drop_block"}},"output_config":{"effort":"high"},"anthropic_beta":["thinking-binding-controls-2026-08-01"]}`
			}
			assertShapeJSON(t, data, want)
		})
	}
}

func BenchmarkBedrockThinkingFields(b *testing.B) {
	model := &Model{ID: "global.anthropic.claude-opus-5", DisplayName: "Claude Opus 5"}
	options := StreamOptions{Thinking: ThinkingLevelXHigh, IsReasoning: true, Region: "us-gov-west-1"}
	b.ReportAllocs()
	for b.Loop() {
		buildBedrockAdditionalFields(model, model.DisplayName, options)
	}
}

// upstream: bedrock-converse-stream.ts:1266 `options.thinkingDisplay ?? "summarized"`, for adaptive and budget thinking; GovCloud still omits it.
func TestBedrockThinkingDisplayOptionTargets(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		id      string
		option  BedrockThinkingDisplay
		region  string
		display any
	}{
		{"adaptive omitted", "global.anthropic.claude-opus-5", AnthropicThinkingDisplayOmitted, "us-east-1", "omitted"},
		{"adaptive default", "global.anthropic.claude-opus-5", "", "us-east-1", "summarized"},
		{"budget omitted", "anthropic.claude-3-7-sonnet-20250219-v1:0", AnthropicThinkingDisplayOmitted, "us-east-1", "omitted"},
		{"gov cloud keeps none", "global.anthropic.claude-opus-5", AnthropicThinkingDisplayOmitted, "us-gov-west-1", nil},
	} {
		fields := buildBedrockAdditionalFields(&Model{ID: row.id, DisplayName: "Claude Opus 5"}, "Claude Opus 5", StreamOptions{Thinking: ThinkingLevelHigh, IsReasoning: true, Region: row.region, ThinkingDisplay: row.option})
		thinking, _ := fields["thinking"].(map[string]any)
		if got := thinking["display"]; got != row.display {
			t.Errorf("%s: display = %v, want %v", row.name, got, row.display)
		}
	}
}
