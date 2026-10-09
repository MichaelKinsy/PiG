package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestSubprocessPreservesNativeThinkingOptions(t *testing.T) {
	options, err := subprocessStreamOptions(map[string]any{"thinking": map[string]any{"enabled": true, "budgetTokens": float64(0)}, "reasoning": "high", "reasoningEffort": "xhigh"}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.Thinking != ai.ThinkingLevelHigh || options.ReasoningEffort != "xhigh" || options.GoogleThinking == nil || !options.GoogleThinking.Enabled || options.GoogleThinking.BudgetTokens == nil || *options.GoogleThinking.BudgetTokens != 0 {
		t.Fatalf("options = %+v", options)
	}
}

func TestSubprocessPreservesAnthropicAndBedrockOptions(t *testing.T) {
	options, err := subprocessStreamOptions(map[string]any{"thinkingEnabled": true, "thinkingBudgetTokens": float64(2048), "effort": "medium", "interleavedThinking": false, "requestMetadata": map[string]any{"app": "pi-test", "env": "ci"}}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if options.ThinkingEnabled == nil || !*options.ThinkingEnabled || options.ThinkingBudgetTokens == nil || *options.ThinkingBudgetTokens != 2048 || options.Effort != "medium" || options.InterleavedThinking == nil || *options.InterleavedThinking || options.RequestMetadata["app"] != "pi-test" || options.RequestMetadata["env"] != "ci" {
		t.Fatalf("native options = %+v", options)
	}
}

// Pi BedrockOptions.thinkingDisplay (bedrock-converse-stream.ts:99) reaches the provider from an extension's model request.
func TestSubprocessPreservesBedrockThinkingDisplay(t *testing.T) {
	options, err := subprocessStreamOptions(map[string]any{"thinkingDisplay": "omitted"}, ai.StreamOptions{})
	if err != nil || options.ThinkingDisplay != ai.AnthropicThinkingDisplayOmitted {
		t.Fatalf("options = %+v, err = %v", options, err)
	}
	if _, err := subprocessStreamOptions(map[string]any{"thinkingDisplay": 1.0}, ai.StreamOptions{}); err == nil {
		t.Fatal("a non-string thinkingDisplay must be rejected")
	}
}
