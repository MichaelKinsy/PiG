package ai

import (
	"encoding/json"
	"testing"
)

// upstream: packages/ai/src/types.ts:707-715. The config is `{type:"json_schema", strict:"prefer"|"require"} | {type:"grammar", variants}`; the typed discriminator constants serialize to exactly those wire values and decode back to them.
func TestConstrainedSamplingConfigDiscriminatorWireValues(t *testing.T) {
	for _, row := range []struct {
		config ConstrainedSamplingConfig
		wire   string
	}{
		{ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: ConstrainedSamplingStrictPrefer}, `{"type":"json_schema","strict":"prefer"}`},
		{ConstrainedSamplingConfig{Type: ConstrainedSamplingJSONSchema, Strict: ConstrainedSamplingStrictRequire}, `{"type":"json_schema","strict":"require"}`},
		{ConstrainedSamplingConfig{Type: ConstrainedSamplingGrammar, Variants: GrammarVariants{GrammarFormatOpenAILark: "start: A"}}, `{"type":"grammar","variants":{"openai_lark":"start: A"}}`},
	} {
		got, err := json.Marshal(row.config)
		if err != nil || string(got) != row.wire {
			t.Fatalf("Marshal(%+v) = %s, %v; want %s", row.config, got, err, row.wire)
		}
		var back ConstrainedSamplingConfig
		if err := json.Unmarshal([]byte(row.wire), &back); err != nil || back.Type != row.config.Type || back.Strict != row.config.Strict {
			t.Fatalf("Unmarshal(%s) = %+v, %v", row.wire, back, err)
		}
	}
}
