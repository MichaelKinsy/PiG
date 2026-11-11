package ai

import "testing"

// Pi 842c7317 (packages/ai/src/api/constrained-sampling.ts): the helpers the Anthropic Messages and Bedrock fallbacks share.
func TestPrefersJSONSchemaStrictSampling(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool ToolSchema
		want bool
	}{
		{"no constrained sampling", ToolSchema{Name: "plain"}, false},
		{"json_schema prefer", ToolSchema{ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: ConstrainedSamplingStrictPrefer}}, true},
		{"json_schema require", ToolSchema{ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: ConstrainedSamplingStrictRequire}}, false},
		{"json_schema without strict", ToolSchema{ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema}}, false},
		{"grammar", ToolSchema{ConstrainedSampling: &ConstrainedSamplingConfig{Type: ConstrainedSamplingGrammar, Strict: ConstrainedSamplingStrictPrefer}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := prefersJSONSchemaStrictSampling(tc.tool); got != tc.want {
				t.Fatalf("prefersJSONSchemaStrictSampling = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestIsStrictToolLimitErrorMessage(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    bool
	}{
		{"The compiled grammar is too large, which would cause performance issues.", true},
		{"Too many strict tools (21). The maximum number of strict tools is 20.", true},
		{"The model returned the following errors: Schemas contains too many parameters with union types (20 parameters with type arrays or anyOf).", true},
		{"messages: field required", false},
		{"Too many strict tools", false},
		{"", false},
	} {
		if got := isStrictToolLimitErrorMessage(tc.message); got != tc.want {
			t.Errorf("isStrictToolLimitErrorMessage(%q) = %t, want %t", tc.message, got, tc.want)
		}
	}
}
