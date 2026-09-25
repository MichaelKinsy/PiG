package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ports openai-responses-empty-tool-result.test.ts. An empty tool result (a
// command that produced no output) must become the "(no tool output)"
// placeholder on the openai-responses function_call_output, never an empty
// string and never an image placeholder. Mirrors upstream
// openai-responses-shared.ts convertToolResultOutput.
func TestResponsesConvertMessages_EmptyToolResultUsesPlaceholder(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-4o-mini"}}
	messages := []Message{
		UserMessage{Content: UserText("Run the command")},
		AssistantMessage{Content: []AssistantContentBlock{
			ToolCall{ID: "tool-1", Name: "bash", Arguments: JsonObject{"command": "true"}},
		}},
		ToolResultMessage{ToolCallID: "tool-1", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{}}},
	}

	items, _ := p.convertMessages(messages, nil)

	var fco *respInputItem
	for i := range items {
		if items[i].Type == "function_call_output" {
			fco = &items[i]
		}
	}
	if fco == nil {
		t.Fatal("no function_call_output item produced")
	}
	var got string
	if err := json.Unmarshal(fco.Output, &got); err != nil {
		t.Fatalf("output is not a JSON string: %v (raw %s)", err, fco.Output)
	}
	if got != "(no tool output)" {
		t.Errorf("empty tool-result output = %q, want %q", got, "(no tool output)")
	}
	if strings.Contains(got, "see attached image") {
		t.Errorf("empty tool-result output %q must not mention an image", got)
	}
}

// Ports the observable payload-shape contract of openai-responses-tool-result-images.test.ts
// (the upstream test is live/E2E; this drives production convertMessages, no network).
// Tool-result images on an image-capable responses model must ride inside the
// function_call_output as an input_text + input_image array, not be dropped and
// not spill into a trailing user turn.
func TestResponsesConvertMessages_ToolResultImagesStayInFunctionCallOutput(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-4o-mini"}}
	const toolText = "A red circle with a diameter of 100 pixels."
	messages := []Message{
		UserMessage{Content: UserText("Call the tool and describe the image.")},
		AssistantMessage{Content: []AssistantContentBlock{
			ToolCall{ID: "tool-1", Name: "get_circle", Arguments: JsonObject{}},
		}},
		ToolResultMessage{
			ToolCallID: "tool-1",
			ToolName:   "get_circle",
			Content: []ToolResultMessageContent{
				TextContent{Text: toolText},
				ImageContent{MimeType: "image/png", Data: "aGVsbG8="},
			},
		},
	}

	items, _ := p.convertMessages(messages, nil)

	fcoIdx := -1
	for i := range items {
		if items[i].Type == "function_call_output" {
			fcoIdx = i
		}
	}
	if fcoIdx < 0 {
		t.Fatal("no function_call_output item produced")
	}

	var parts []map[string]string
	if err := json.Unmarshal(items[fcoIdx].Output, &parts); err != nil {
		t.Fatalf("output is not a content array: %v (raw %s)", err, items[fcoIdx].Output)
	}
	var text, image map[string]string
	for _, part := range parts {
		switch part["type"] {
		case "input_text":
			text = part
		case "input_image":
			image = part
		}
	}
	if text == nil {
		t.Fatal("no input_text part in function_call_output")
	}
	if !strings.Contains(text["text"], toolText) {
		t.Errorf("input_text = %q, want to contain %q", text["text"], toolText)
	}
	if image == nil {
		t.Fatal("no input_image part in function_call_output (image was dropped)")
	}
	if !strings.HasPrefix(image["image_url"], "data:image/png;base64,") {
		t.Errorf("input_image url = %q, want data:image/png;base64, prefix", image["image_url"])
	}

	// No later user turn should carry the image (it stays in the tool output).
	for _, it := range items[fcoIdx+1:] {
		if it.Role == "user" {
			t.Errorf("image spilled into a trailing user turn: %s", it.Content)
		}
	}
}
