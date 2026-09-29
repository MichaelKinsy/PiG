package ai

import (
	"encoding/json"
	"testing"
)

// packages/ai/src/api/anthropic-messages.ts:1270-1355 uses ECMAScript trim: BOM is whitespace, NEL is not. Keep the original text/signature bytes when a block survives.
func TestAnthropicECMAScriptWhitespace(t *testing.T) {
	for _, tc := range []struct {
		name       string
		message    Message
		allowEmpty bool
		want       string
	}{
		{"user string BOM", UserMessage{Content: UserText("\ufeff")}, false, `[]`},
		{"user string NEL", UserMessage{Content: UserText("\u0085")}, false, `[{"role":"user","content":"\u0085"}]`},
		{"user block BOM", UserMessage{Content: UserContentBlocks{TextContent{Text: "\ufeff"}}}, false, `[]`},
		{"user block NEL", UserMessage{Content: UserContentBlocks{TextContent{Text: "\u0085"}}}, false, `[{"role":"user","content":[{"type":"text","text":"\u0085"}]}]`},
		{"assistant text BOM", AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "\ufeff"}}}, false, `[]`},
		{"assistant text NEL", AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "\u0085"}}}, false, `[{"role":"assistant","content":[{"type":"text","text":"\u0085"}]}]`},
		{"thinking BOM", AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "\ufeff"}}}, true, `[]`},
		{"thinking NEL unsigned enabled", AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "\u0085"}}}, true, `[{"role":"assistant","content":[{"type":"thinking","thinking":"\u0085","signature":""}]}]`},
		{"thinking NEL unsigned disabled", AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "\u0085"}}}, false, `[{"role":"assistant","content":[{"type":"text","text":"\u0085"}]}]`},
		{"signature BOM unsigned enabled", AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "internal", ThinkingSignature: "\ufeff"}}}, true, `[{"role":"assistant","content":[{"type":"thinking","thinking":"internal","signature":""}]}]`},
		{"signature BOM unsigned disabled", AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "internal", ThinkingSignature: "\ufeff"}}}, false, `[{"role":"assistant","content":[{"type":"text","text":"internal"}]}]`},
		{"signature NEL preserved", AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "internal", ThinkingSignature: "\u0085"}}}, true, `[{"role":"assistant","content":[{"type":"thinking","thinking":"internal","signature":"\u0085"}]}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := tc.message
			if assistant, ok := message.(AssistantMessage); ok {
				assistant.API = APIAnthropicMessages
				assistant.Provider = "xiaomi-token-plan-ams"
				assistant.Model = "mimo-v2.5-pro"
				assistant.StopReason = StopReasonStop
				message = assistant
			}
			t.Run("converter", func(t *testing.T) {
				converted := anthConvertMessages([]Message{message}, false, tc.allowEmpty, false)
				encoded, err := json.Marshal(converted)
				if err != nil {
					t.Fatal(err)
				}
				assertShapeJSON(t, encoded, tc.want)
			})
			t.Run("provider caller", func(t *testing.T) {
				model := emptySignatureModel(&ModelCompat{AllowEmptySignature: new(tc.allowEmpty)})
				payload := upstreamAnthropicParams(t, model, Context{Messages: []Message{message}}, StreamOptions{CacheRetention: CacheRetentionNone})
				assertShapeJSON(t, payload["messages"], tc.want)
			})
		})
	}
}
