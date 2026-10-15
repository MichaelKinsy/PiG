package codingagent

import (
	"maps"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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

// An extension's model request carries Pi's thinkingBudgets object; a present level is configured whatever its value, so a zero budget reaches the provider as zero instead of the default.
func TestSubprocessStreamOptionsKeepConfiguredZeroThinkingBudget(t *testing.T) {
	options, err := subprocessStreamOptions(map[string]any{"thinkingBudgets": map[string]any{"medium": float64(0), "high": float64(-1)}}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for level, want := range map[string]int{"medium": 0, "high": -1, "low": ai.DefaultThinkingBudgets().Low} {
		if got := ai.ThinkingBudgetForLevel(level, options.ThinkingBudgets); got != want {
			t.Errorf("%s budget = %d, want %d", level, got, want)
		}
	}
	empty, err := subprocessStreamOptions(map[string]any{"thinkingBudgets": map[string]any{}}, ai.StreamOptions{})
	if err != nil || empty.ThinkingBudgets == nil || empty.ThinkingBudgets.Set != 0 {
		t.Fatalf("empty thinkingBudgets = %#v, %v", empty.ThinkingBudgets, err)
	}
}

// The subprocess bridge decodes requestMetadata as an ordered object; its key order reaches StreamOptions.RequestMetadataOrder, which
// the Bedrock body writer keeps on the wire.
func TestSubprocessRequestMetadataKeepsKeyOrder(t *testing.T) {
	metadata, err := orderedjson.Parse([]byte(`{"zeta":"1","alpha":"2","10":"3"}`))
	if err != nil {
		t.Fatal(err)
	}
	options, err := subprocessStreamOptions(map[string]any{"requestMetadata": metadata}, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(options.RequestMetadataOrder, []string{"10", "zeta", "alpha"}) || !maps.Equal(options.RequestMetadata, map[string]string{"zeta": "1", "alpha": "2", "10": "3"}) {
		t.Fatalf("metadata = %v, order = %q", options.RequestMetadata, options.RequestMetadataOrder)
	}
	for _, raw := range []string{`{"a":1}`, `{"a":null}`} {
		bad, err := orderedjson.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := subprocessStreamOptions(map[string]any{"requestMetadata": bad}, ai.StreamOptions{}); err == nil || err.Error() != "model request.requestMetadata.a must be a string" {
			t.Fatalf("%s: err = %v", raw, err)
		}
	}
}
