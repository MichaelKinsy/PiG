package ai

import (
	"reflect"
	"testing"
)

// D115: Gemini answers the function calls of one model turn only when one user turn directly after it holds every function response ("the number of function response parts must equal the number of function call parts"). Pi 1.1.0 (google-shared.ts convertMessages, asserted by google-shared-image-tool-result-routing.test.ts:80-88) pushes the image turn of a Gemini 2.x tool result before the next result, which splits the response turn. PiG sends the image turns after the last result of the run, in result order.
func TestGoogleImageToolResultsKeepFunctionResponsesInOneTurnD115(t *testing.T) {
	image := func(data string) ImageContent { return ImageContent{MimeType: "image/png", Data: data} }
	result := func(id string, content ...ToolResultMessageContent) ToolResultMessage {
		return ToolResultMessage{ToolCallID: id, ToolName: "read", Content: content}
	}
	calls := AssistantMessage{API: APIGoogleGenerativeAI, Provider: "google", Model: "gemini-2.5-flash", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
		ToolCall{ID: "a", Name: "read", Arguments: JsonObject{}}, ToolCall{ID: "b", Name: "read", Arguments: JsonObject{}}, ToolCall{ID: "c", Name: "read", Arguments: JsonObject{}},
	}}
	messages := []Message{
		UserMessage{Content: UserText("go")},
		calls,
		result("a", TextContent{Text: "A"}, image("IMG-A")),
		result("b", TextContent{Text: "B"}),
		result("c", TextContent{Text: "C"}, image("IMG-C1"), image("IMG-C2")),
		UserMessage{Content: UserText("next")},
	}
	contents := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)

	var shape []string
	for _, content := range contents {
		kind := content.Role + ":"
		for _, part := range content.Parts {
			switch {
			case part.FunctionResponse != nil:
				kind += "R"
			case part.InlineData != nil:
				kind += "I"
			case part.Text != nil && *part.Text == "Tool result image:":
				kind += "T"
			default:
				kind += "t"
			}
		}
		shape = append(shape, kind)
	}
	// The model turn's three function calls report as "t": they carry neither a response nor an image.
	want := []string{"user:t", "model:ttt", "user:RRR", "user:TI", "user:TII", "user:t"}
	if !reflect.DeepEqual(shape, want) {
		t.Fatalf("turn shapes = %v, want %v", shape, want)
	}
	if first := contents[3].Parts[1].InlineData.Data; first != "IMG-A" {
		t.Fatalf("first image turn holds %q, want the first result's image", first)
	}
	if last := contents[4].Parts[2].InlineData.Data; last != "IMG-C2" {
		t.Fatalf("second image turn ends with %q, want the third result's last image", last)
	}
}

// A single call keeps Pi's shape: its image turn follows its function response (google-shared-image-tool-result-routing.test.ts).
func TestGoogleImageToolResultOfASingleCallMatchesPiD115(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserText("go")},
		AssistantMessage{API: APIGoogleGenerativeAI, Provider: "google", Model: "gemini-2.5-flash", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "a", Name: "read", Arguments: JsonObject{}}}},
		ToolResultMessage{ToolCallID: "a", ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "A"}, ImageContent{MimeType: "image/png", Data: "IMG"}}},
	}
	contents := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	if len(contents) != 4 || contents[2].Parts[0].FunctionResponse == nil || contents[3].Parts[0].Text == nil || *contents[3].Parts[0].Text != "Tool result image:" {
		t.Fatalf("contents = %+v", contents)
	}
}

// google-shared-image-tool-result-routing.test.ts:105 (Pi 4dd2b149, #8720): whitespace-only tool result text counts as no
// text, so a result of only "\r\n" answers "" and an image result with " \n" before its image answers the image placeholder.
func TestGoogleWhitespaceOnlyToolResultTextIsNoText(t *testing.T) {
	result := func(id string, content ...ToolResultMessageContent) ToolResultMessage {
		return ToolResultMessage{ToolCallID: id, ToolName: "read", Content: content}
	}
	messages := []Message{
		UserMessage{Content: UserText("read the files")},
		AssistantMessage{API: APIGoogleGenerativeAI, Provider: "google", Model: "gemini-3-pro-preview", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{
			ToolCall{ID: "call_a", Name: "read", Arguments: JsonObject{"path": "a.txt"}},
			ToolCall{ID: "call_img", Name: "read", Arguments: JsonObject{"path": "image.png"}},
			ToolCall{ID: "call_b", Name: "read", Arguments: JsonObject{"path": "b.txt"}},
		}},
		result("call_a", TextContent{Text: "\r\n"}),
		result("call_img", TextContent{Text: " \n"}, ImageContent{MimeType: "image/png", Data: "abc"}),
		result("call_b", TextContent{Text: "beta text"}),
	}
	contents := geminiConvertMessages(messages, "google", "gemini-3-pro-preview", true)
	var responses []any
	for _, part := range contents[2].Parts {
		responses = append(responses, part.FunctionResponse.Response)
	}
	want := []any{map[string]any{"output": ""}, map[string]any{"output": "(see attached image)"}, map[string]any{"output": "beta text"}}
	if !reflect.DeepEqual(responses, want) {
		t.Fatalf("responses = %v, want %v", responses, want)
	}
}
