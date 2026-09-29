package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// packages/ai/src/api/anthropic-messages.ts:1206-1208 replaces each UTF-16 code unit with a non-u regexp, then slices the ASCII result to 64 units.
func TestAnthropicToolCallIDUTF16Normalization(t *testing.T) {
	if got := normalizeAnthropicToolCallID(""); got != "" {
		t.Fatalf("empty ID = %q", got)
	}
	for _, tc := range []struct{ name, id, want string }{
		{"ASCII", "call_A-1", "call_A-1"},
		{"punctuation", "a|b c", "a_b_c"},
		{"BMP", "工具", "__"},
		{"astral", "call_😀_tail", "call_" + "__" + "_tail"},
		{"several astral", "😀𐐀𝄞", "______"},
		{"astral at 62", strings.Repeat("x", 62) + "😀tail", strings.Repeat("x", 62) + "__"},
		{"astral at 63", strings.Repeat("x", 63) + "😀tail", strings.Repeat("x", 63) + "_"},
		{"astral after cap", strings.Repeat("x", 64) + "😀tail", strings.Repeat("x", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("normalizer", func(t *testing.T) {
				if got := normalizeAnthropicToolCallID(tc.id); got != tc.want {
					t.Fatalf("ID=%q, want %q", got, tc.want)
				}
			})
			for _, foreign := range []bool{false, true} {
				name := "same model"
				if foreign {
					name = "foreign model"
				}
				t.Run(name, func(t *testing.T) {
					model := emptySignatureModel(nil)
					historyModel := model.ID
					if foreign {
						historyModel = "other-model"
					}
					input := Context{Messages: []Message{
						UserMessage{Content: UserText("use tool")},
						AssistantMessage{API: APIAnthropicMessages, Provider: model.ProviderMeta.ProviderID, Model: historyModel, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: tc.id, Name: "lookup", Arguments: JsonObject{}}}},
						ToolResultMessage{ToolCallID: tc.id, ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
					}}
					payload := upstreamAnthropicParams(t, model, input, StreamOptions{CacheRetention: CacheRetentionNone})
					var messages []struct {
						Content json.RawMessage `json:"content"`
					}
					if err := json.Unmarshal(payload["messages"], &messages); err != nil {
						t.Fatal(err)
					}
					if len(messages) != len(input.Messages) {
						t.Fatalf("messages=%s", payload["messages"])
					}
					var calls []struct {
						ID string `json:"id"`
					}
					var results []struct {
						ID string `json:"tool_use_id"`
					}
					if err := json.Unmarshal(messages[1].Content, &calls); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(messages[2].Content, &results); err != nil {
						t.Fatal(err)
					}
					if len(calls) != 1 || len(results) != 1 || calls[0].ID != tc.want || results[0].ID != tc.want {
						t.Fatalf("call=%v result=%v want %q", calls, results, tc.want)
					}
					if input.Messages[1].(AssistantMessage).Content[0].(ToolCall).ID != tc.id || input.Messages[2].(ToolResultMessage).ToolCallID != tc.id {
						t.Fatal("normalization mutated retained history")
					}
				})
			}
		})
	}
}
