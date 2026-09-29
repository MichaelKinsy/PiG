package ai

import (
	"reflect"
	"testing"
)

// api/openai-completions.ts:878-885 distinguishes a null native effort mapping from an omitted mapping and preserves thinking across tool turns.
func TestRawZaiEffortNullMappingAndThinkingReplay(t *testing.T) {
	for _, effort := range []string{"xhigh", "high", "max"} {
		t.Run(effort, func(t *testing.T) {
			request := captureOpenAIRequestMap(t, "zai", "glm-5.2", &OpenAICompat{ThinkingFormat: "zai", SupportsReasoningEffort: new(true)}, StreamOptions{IsReasoning: true, ReasoningEffort: effort})
			if got := request["thinking"]; !reflect.DeepEqual(got, map[string]any{"type": "enabled", "clear_thinking": false}) {
				t.Errorf("thinking = %#v", got)
			}
			got, exists := request["reasoning_effort"]
			if effort == "xhigh" {
				if exists {
					t.Errorf("null-mapped effort sent: %#v", got)
				}
			} else if got != effort {
				t.Errorf("effort = %#v, want %s", got, effort)
			}
		})
	}
}
