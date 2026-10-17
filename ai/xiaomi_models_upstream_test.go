package ai

import (
	"slices"
	"testing"
)

// Pi 6374c2ba (#10818): packages/ai/test/xiaomi-models.test.ts "models %s reasoning as an off/on toggle". MiMo Chat Completions only supports a thinking on/off toggle.
func TestXiaomiReasoningModelsAreAnOffOnToggleUpstream(t *testing.T) {
	for _, provider := range []string{"xiaomi", "xiaomi-token-plan-cn", "xiaomi-token-plan-ams", "xiaomi-token-plan-sgp"} {
		t.Run(provider, func(t *testing.T) {
			reasoning := 0
			for _, model := range GetBuiltinModels(provider) {
				if !model.ProviderMeta.Reasoning {
					continue
				}
				reasoning++
				if got := GetSupportedThinkingLevels(model); !slices.Equal(got, []ModelThinkingLevel{ThinkingOff, ThinkingHigh}) {
					t.Errorf("%s supported levels = %v, want [off high]", model.ID, got)
				}
				model.ProviderMeta.API = APIOpenAICompletions
				for level, want := range map[ThinkingLevel]string{ThinkingLevelHigh: `{"type":"enabled"}`, "": `{"type":"disabled"}`} {
					payload, _, _ := captureToolChoiceRequest(t, model, Context{Messages: []Message{UserMessage{Content: UserText("test")}}}, StreamOptions{Thinking: level}, nil)
					requireToolChoiceField(t, payload, "thinking", want)
					forbidToolChoiceFields(t, payload, "reasoning_effort")
				}
			}
			if reasoning == 0 {
				t.Fatalf("%s has no reasoning models", provider)
			}
		})
	}
}
