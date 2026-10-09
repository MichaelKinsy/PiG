//go:build !pig_strip_mistral_conversations

package ai

import (
	"reflect"
	"testing"
)

// ecmaScriptBlankCases separate String.prototype.trim from Go's Unicode White_Space: BOM is ECMAScript whitespace but not Unicode White_Space, and NEL is the reverse.
var ecmaScriptBlankCases = []struct {
	name, text string
	blank      bool
}{
	{"ASCII", " \t\r\n", true},
	{"NBSP", "\u00a0", true},
	{"BOM", "\ufeff", true},
	{"BOM with spaces", " \ufeff\t", true},
	{"NEL", "\u0085", false},
	{"NEL with spaces", " \u0085 ", false},
}

func ecmaScriptBlankBlocks(text string) map[string]AssistantContentBlock {
	return map[string]AssistantContentBlock{
		"text":     TextContent{Text: text},
		"thinking": ThinkingContent{Thinking: text, ThinkingSignature: "reasoning_content"},
	}
}

func TestOpenAICompletionsAssistantBlankUsesECMAScriptTrim(t *testing.T) {
	// Pi openai-completions.ts:1290-1292 and 1313 filter text and thinking blocks by block.trim().length. The tool call keeps the message itself, which 1385-1395 would otherwise skip without content, so the trim alone decides each field.
	for _, tc := range ecmaScriptBlankCases {
		for kind, block := range ecmaScriptBlankBlocks(tc.text) {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				call := ToolCall{ID: "call", Name: "tool", Arguments: JsonObject{}}
				out, err := convertMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{block, call}}}, false, nil)
				if err != nil {
					t.Fatal(err)
				}
				var got []string
				for _, message := range out {
					if content, ok := message.Content.(string); ok && content != "" {
						got = append(got, content)
					}
					if message.ReasoningContent != nil {
						got = append(got, *message.ReasoningContent)
					}
				}
				assertECMAScriptBlank(t, got, tc.text, tc.blank)
			})
		}
	}
}

func TestOpenAICompletionsSkipsReasoningOnlyAssistantMessages(t *testing.T) {
	// Pi openai-completions.ts:1385-1395 skips an assistant message with no content and no tool_calls, whatever reasoning fields it carries.
	for _, signature := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
		out, err := convertMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "reason", ThinkingSignature: signature}}}}, false, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 0 {
			t.Errorf("signature %s: sent %#v, want no assistant message", signature, out)
		}
	}
}

func TestMistralAssistantBlankUsesECMAScriptTrim(t *testing.T) {
	// Pi mistral-conversations.ts:819-832 keeps text and thinking blocks only when block.trim().length > 0.
	provider := &mistralProvider{}
	for _, tc := range ecmaScriptBlankCases {
		for kind, block := range ecmaScriptBlankBlocks(tc.text) {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				var got []string
				for _, message := range provider.convertMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{block}}}, false) {
					chunks, _ := message.Content.([]mistralContentChunk)
					for _, chunk := range chunks {
						if chunk.Text != nil {
							got = append(got, *chunk.Text)
						}
						if thinking, ok := chunk.Thinking.([]map[string]string); ok {
							for _, part := range thinking {
								got = append(got, part["text"])
							}
						}
					}
				}
				assertECMAScriptBlank(t, got, tc.text, tc.blank)
			})
		}
	}
}

func TestMistralToolResultTrimsWithECMAScriptTrim(t *testing.T) {
	// Pi mistral-conversations.ts:877-883 sends text.trim() and uses its length for the empty-result fallback.
	provider := &mistralProvider{}
	for _, tc := range []struct{ name, text, want string }{
		{"BOM", "\ufeffresult\ufeff", "result"},
		{"NEL", "\u0085result\u0085", "\u0085result\u0085"},
		{"BOM only", "\ufeff", "(no tool output)"},
		{"NEL only", "\u0085", "\u0085"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := provider.convertMessages([]Message{ToolResultMessage{ToolCallID: "call", ToolName: "tool", Content: []ToolResultMessageContent{TextContent{Text: tc.text}}}}, false)
			if len(messages) != 1 {
				t.Fatalf("messages=%#v", messages)
			}
			chunks, _ := messages[0].Content.([]mistralContentChunk)
			if len(chunks) != 1 || chunks[0].Text == nil || *chunks[0].Text != tc.want {
				t.Fatalf("tool content=%#v, want %q", messages[0].Content, tc.want)
			}
		})
	}
}

func TestMistralSystemUpdateKeepsNonemptyWhitespace(t *testing.T) {
	// Pi mistral-conversations.ts:787-790 sends a later system update whenever its rendered text has length > 0; it does not trim.
	provider := &mistralProvider{}
	for _, text := range []string{" \t", "\ufeff", "\u0085", "update"} {
		t.Run(text, func(t *testing.T) {
			messages := provider.convertMessages([]Message{SystemMessage{Content: SystemText(text)}}, false)
			if want := []mistralMessage{{Role: "system", Content: text}}; !reflect.DeepEqual(messages, want) {
				t.Fatalf("messages=%#v, want %#v", messages, want)
			}
		})
	}
	if messages := provider.convertMessages([]Message{SystemMessage{Content: SystemText("")}}, false); len(messages) != 0 {
		t.Fatalf("empty update=%#v, want none", messages)
	}
}

func TestGoogleAssistantBlankUsesECMAScriptTrim(t *testing.T) {
	// Pi google-shared.ts:240, 252 and 260 drop unsigned text and thinking only when block.trim() === "".
	for _, tc := range ecmaScriptBlankCases {
		for _, source := range []string{"google", "openai"} {
			for kind, block := range ecmaScriptBlankBlocks(tc.text) {
				t.Run(tc.name+"/"+source+"/"+kind, func(t *testing.T) {
					message := AssistantMessage{Provider: source, Model: "gemini-2.5-flash", Content: []AssistantContentBlock{block}}
					var got []string
					for _, content := range geminiConvertMessages([]Message{message}, "google", "gemini-2.5-flash", true) {
						for _, part := range content.Parts {
							if part.Text != nil {
								got = append(got, *part.Text)
							}
						}
					}
					assertECMAScriptBlank(t, got, tc.text, tc.blank)
				})
			}
		}
	}
}

func assertECMAScriptBlank(t *testing.T, got []string, text string, blank bool) {
	t.Helper()
	var want []string
	if !blank {
		want = []string{text}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sent %q, want %q", got, want)
	}
}
