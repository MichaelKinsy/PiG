//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A configured budget wins over the default whatever its value (bedrock-converse-stream.ts:1301 uses ??), so zero is a real setting in the Bedrock path.
func TestBedrockConfiguredThinkingBudgetsWinWhateverTheirValue(t *testing.T) {
	zeroMedium := ai.NewThinkingBudgets(nil, nil, new(0), nil)
	claude := &ai.Model{ID: "anthropic.claude-3-7-sonnet-20250219-v1:0", Capabilities: ai.ModelCapabilities{MaxOutputTokens: 32768}}
	for name, test := range map[string]struct {
		budgets *ai.ThinkingBudgets
		level   ai.ModelThinkingLevel
		want    int
	}{"zero": {zeroMedium, ai.ThinkingMedium, 0}, "default": {nil, ai.ThinkingMedium, 8192}, "xhigh": {ai.NewThinkingBudgets(nil, nil, nil, new(0)), ai.ThinkingXHigh, 0}} {
		fields := buildBedrockAdditionalFieldsWithBudget(claude, "", ai.StreamOptions{IsReasoning: true, Thinking: ai.ThinkingLevel(test.level), ThinkingBudgets: test.budgets}, nil)
		thinking, _ := fields["thinking"].(map[string]any)
		if thinking["budget_tokens"] != test.want {
			t.Errorf("bedrock %s: budget_tokens %v, want %d (%v)", name, thinking["budget_tokens"], test.want, fields)
		}
	}
}
