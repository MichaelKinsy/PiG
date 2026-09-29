package modelgen

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestModelsDevReasoningOptionsUpstream(t *testing.T) {
	cases := []struct{ name, input, want string }{
		// .upstream/v0.87.1/packages/ai/test/reasoning-options.test.ts:5
		{"exposes only verified effort values and none", `[{"type":"toggle"},{"type":"effort","values":["none","low","high","max"]}]`, `{"off":"none","minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// .upstream/v0.87.1/packages/ai/test/reasoning-options.test.ts:19
		{"does not infer thinking-off from an effort list", `[{"type":"effort","values":["low","high","max"]}]`, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// .upstream/v0.87.1/packages/ai/test/reasoning-options.test.ts:31 — all three assertions.
		{"leaves toggle and budget controls for their adapter-specific implementations/toggle", `[{"type":"toggle"}]`, `null`},
		{"leaves toggle and budget controls for their adapter-specific implementations/budget", `[{"type":"budget_tokens","min":1024,"max":32000}]`, `null`},
		{"leaves toggle and budget controls for their adapter-specific implementations/defaults", `[{"type":"effort","values":[null,"default"]}]`, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var options []ModelsDevReasoningOption
			if err := json.Unmarshal([]byte(tc.input), &options); err != nil {
				t.Fatal(err)
			}
			var want map[string]*string
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			got := GetEffortThinkingLevelMap(options)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("map=%v want=%v", got, want)
			}
		})
	}
}

func TestOpenRouterReasoningMetadataUpstream(t *testing.T) {
	cases := []struct{ name, input, want string }{
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:43
		{"marks mandatory reasoning and unsupported efforts unavailable", `{"mandatory":true,"default_enabled":true,"supported_efforts":["max","high","low"],"default_effort":"max"}`, `{"off":null,"minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":"max"}`},
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:62
		{"still marks off unavailable when OpenRouter omits effort metadata", `{"mandatory":true}`, `{"off":null}`},
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:66
		{"keeps off available while restricting optional models to supported efforts", `{"mandatory":false,"default_enabled":true,"supported_efforts":["high","low"]}`, `{"off":"none","minimal":null,"low":"low","medium":null,"high":"high","xhigh":null,"max":null}`},
		// .upstream/v0.87.1/packages/ai/test/openrouter-reasoning-options.test.ts:84
		{"does not add metadata for optional models without effort controls", `{"mandatory":false}`, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var metadata *OpenRouterReasoningMetadata
			if err := json.Unmarshal([]byte(tc.input), &metadata); err != nil {
				t.Fatal(err)
			}
			var want map[string]*string
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if got := GetOpenRouterThinkingLevelMap(metadata); !reflect.DeepEqual(got, want) {
				t.Fatalf("got=%v want=%v", got, want)
			}
		})
	}
}
