package ai

import (
	"encoding/json"
	"testing"
)

// Ports packages/ai/test/anthropic-error-tool-result-images.test.ts (Pi 5ab7fe74, #10794): Anthropic rejects an error tool
// result that holds anything but text, so each run of images in it becomes a note and the images follow all tool results of
// the turn in the same user message, under a caption naming the tool call.
func errorImageAssistant() AssistantMessage {
	return AssistantMessage{
		API: APIAnthropicMessages, Provider: "anthropic", Model: "claude-test", StopReason: StopReasonToolUse,
		Content: []AssistantContentBlock{
			ToolCall{ID: "call-1", Name: "codemode", Arguments: JsonObject{}},
			ToolCall{ID: "call-2", Name: "read", Arguments: JsonObject{}},
		},
	}
}

func errorImageModel() *Model {
	return &Model{ID: "claude-test", DisplayName: "Claude Test", Input: []string{"text", "image"}, Capabilities: ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 1024}, ProviderMeta: ProviderMetadata{ProviderID: "anthropic", API: APIAnthropicMessages, BaseURL: "http://127.0.0.1:9"}}
}

func anthropicImageJSON(data string) string {
	return `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + data + `"}}`
}

func TestAnthropicErrorToolResultImages(t *testing.T) {
	// anthropic-error-tool-result-images.test.ts:79 "moves the images after the tool results of the turn"
	t.Run("moves the images after the tool results of the turn", func(t *testing.T) {
		payload := upstreamAnthropicParams(t, errorImageModel(), Context{Messages: []Message{
			UserMessage{Content: UserText("Show the screenshots")},
			errorImageAssistant(),
			ToolResultMessage{ToolCallID: "call-1", ToolName: "codemode", IsError: true, Content: []ToolResultMessageContent{
				TextContent{Text: "Script failed"}, ImageContent{Data: "AAAA", MimeType: "image/png"}, ImageContent{Data: "CCCC", MimeType: "image/png"}, TextContent{Text: "Script error"},
			}},
			ToolResultMessage{ToolCallID: "call-2", ToolName: "read", Content: []ToolResultMessageContent{ImageContent{Data: "BBBB", MimeType: "image/png"}}},
		}}, StreamOptions{CacheRetention: CacheRetentionNone})
		var messages []json.RawMessage
		if err := json.Unmarshal(payload["messages"], &messages); err != nil || len(messages) < 3 {
			t.Fatalf("messages %s: %v", payload["messages"], err)
		}
		assertShapeJSON(t, messages[2], `{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"call-1","content":"Script failed\n(image attached after the tool results)\nScript error","is_error":true},
			{"type":"tool_result","tool_use_id":"call-2","content":[{"type":"text","text":"(see attached image)"},`+anthropicImageJSON("BBBB")+`],"is_error":false},
			{"type":"text","text":"Images from the error result of tool call call-1 (codemode):"},`+anthropicImageJSON("AAAA")+`,`+anthropicImageJSON("CCCC")+`]}`)
	})

	// anthropic-error-tool-result-images.test.ts:121 "keeps error tool results without images unchanged"
	t.Run("keeps error tool results without images unchanged", func(t *testing.T) {
		assistant := errorImageAssistant()
		assistant.Content = assistant.Content[:1]
		payload := upstreamAnthropicParams(t, errorImageModel(), Context{Messages: []Message{
			UserMessage{Content: UserText("Run it")},
			assistant,
			ToolResultMessage{ToolCallID: "call-1", ToolName: "codemode", IsError: true, Content: []ToolResultMessageContent{TextContent{Text: "Script failed"}}},
		}}, StreamOptions{CacheRetention: CacheRetentionNone})
		var messages []json.RawMessage
		if err := json.Unmarshal(payload["messages"], &messages); err != nil || len(messages) < 3 {
			t.Fatalf("messages %s: %v", payload["messages"], err)
		}
		assertShapeJSON(t, messages[2], `{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-1","content":"Script failed","is_error":true}]}`)
	})

	// Go-only rows of the converter that Pi's transformMessages placeholder rule decides: adjacent images share one note,
	// an empty error result is an empty string, and a successful result keeps its images in place.
	image := ImageContent{MimeType: "image/png", Data: "QUJD"}
	for _, test := range []struct {
		name    string
		isError bool
		content []ToolResultMessageContent
		want    string
	}{
		{"errored adjacent images share one note", true, []ToolResultMessageContent{image, image}, `"(image attached after the tool results)"`},
		{"errored empty result", true, []ToolResultMessageContent{}, `""`},
		{"errored text only", true, []ToolResultMessageContent{TextContent{Text: "boom"}}, `"boom"`},
		{"errored image between texts", true, []ToolResultMessageContent{TextContent{Text: "before"}, image, TextContent{Text: "boom"}}, `"before\n(image attached after the tool results)\nboom"`},
		{"successful result keeps its image", false, []ToolResultMessageContent{TextContent{Text: "ok"}, image}, `[{"type":"text","text":"ok"},{"type":"image","source":{"data":"QUJD","media_type":"image/png","type":"base64"}}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			converted := anthConvertMessages([]Message{ToolResultMessage{ToolCallID: "call_1", ToolName: "tool", IsError: test.isError, Content: test.content}}, false, false, false)
			user, ok := converted[0].Content.([]anthContentBlock)
			if !ok {
				t.Fatalf("content %T", converted[0].Content)
			}
			encoded, err := json.Marshal(user[0].Content)
			if err != nil {
				t.Fatal(err)
			}
			assertShapeJSON(t, encoded, test.want)
		})
	}
}
