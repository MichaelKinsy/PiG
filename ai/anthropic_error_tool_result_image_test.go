package ai

import (
	"encoding/json"
	"testing"
)

// earendil-works/pi#10794, fixed in Pi 34a8bb38 (anthropic-messages.ts convertToolResult; Pi adds no test): Anthropic rejects a tool_result with is_error true whose content holds anything but text blocks ("all content must be type `text` if `is_error` is true"), and the rejected result stays in the history so every later turn fails. Each image of an errored tool result is replaced with a text note, in place.
func TestAnthropicErroredToolResultCarriesNoImageBlock(t *testing.T) {
	image := ImageContent{MimeType: "image/png", Data: "QUJD"}
	for _, test := range []struct {
		name    string
		isError bool
		content []ToolResultMessageContent
		want    string
	}{
		{"errored text and image", true, []ToolResultMessageContent{TextContent{Text: "before"}, image, TextContent{Text: "boom"}}, `"before\n[image omitted: error tool results cannot contain images]\nboom"`},
		{"errored image only", true, []ToolResultMessageContent{image}, `"[image omitted: error tool results cannot contain images]"`},
		{"errored adjacent images", true, []ToolResultMessageContent{image, image}, `"[image omitted: error tool results cannot contain images]\n[image omitted: error tool results cannot contain images]"`},
		{"errored empty result", true, []ToolResultMessageContent{}, `""`},
		{"errored text only", true, []ToolResultMessageContent{TextContent{Text: "boom"}}, `"boom"`},
		{"successful result keeps its image", false, []ToolResultMessageContent{TextContent{Text: "ok"}, image}, `[{"type":"text","text":"ok"},{"type":"image","source":{"data":"QUJD","media_type":"image/png","type":"base64"}}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			converted := anthConvertMessages([]Message{ToolResultMessage{ToolCallID: "call_1", IsError: test.isError, Content: test.content}}, false, false, false)
			user, ok := converted[0].Content.([]anthContentBlock)
			if !ok {
				t.Fatalf("content %T", converted[0].Content)
			}
			encoded, err := json.Marshal(user[0].Content)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.want {
				t.Fatalf("tool_result content %s, want %s", encoded, test.want)
			}
		})
	}
}
