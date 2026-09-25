package ai

import (
	"strings"
	"testing"
)

func TestRequiresToolCallId(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4", true}, {"gpt-oss-120b", true}, {"gemini-3-pro", true},
		{"gemini-3-flash", true}, {"gemini-live-3-pro", true}, {"GEMINI-3-PRO", true},
		{"gemini-2.5-flash", false}, {"gemini-2.0-flash", false}, {"gemini-1.5-pro", false},
		{"gpt-4o", false}, {"o3-mini", false},
	}
	for _, test := range tests {
		if got := requiresToolCallId(test.model); got != test.want {
			t.Errorf("requiresToolCallId(%q) = %v, want %v", test.model, got, test.want)
		}
	}
}

func googleToolCallMessages(id string) []Message {
	return []Message{
		AssistantMessage{Provider: "google", Model: "gemini-3-pro", Content: []AssistantContentBlock{
			ToolCall{ID: id, Name: "bash", Arguments: JsonObject{"command": "ls"}},
		}},
		ToolResultMessage{ToolCallID: id, ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
	}
}

func TestGoogleConvertMessages_EmitsToolCallIdWhenRequired(t *testing.T) {
	output := geminiConvertMessages(googleToolCallMessages("call_abc|item_def"), "google", "gemini-3-pro", true)
	const wantID = "call_abc_item_def"
	if call := findFunctionCall(output); call == nil || call.ID != wantID {
		t.Fatalf("function call = %#v", call)
	}
	if response := findFunctionResponse(output); response == nil || response.ID != wantID {
		t.Fatalf("function response = %#v", response)
	}
}

func TestGoogleConvertMessages_OmitsToolCallIdWhenNotRequired(t *testing.T) {
	messages := googleToolCallMessages("call_abc")
	messages[0] = AssistantMessage{Provider: "google", Model: "gemini-2.5-flash", Content: []AssistantContentBlock{
		ToolCall{ID: "call_abc", Name: "bash", Arguments: JsonObject{"command": "ls"}},
	}}
	output := geminiConvertMessages(messages, "google", "gemini-2.5-flash", true)
	if call := findFunctionCall(output); call == nil || call.ID != "" {
		t.Fatalf("function call = %#v", call)
	}
	if response := findFunctionResponse(output); response == nil || response.ID != "" {
		t.Fatalf("function response = %#v", response)
	}
}

func TestGoogleConvertMessages_TruncatesToolCallIdTo64(t *testing.T) {
	id := strings.Repeat("a", 100)
	messages := []Message{AssistantMessage{Provider: "google", Model: "gemini-3-pro", Content: []AssistantContentBlock{
		ToolCall{ID: id, Name: "bash", Arguments: JsonObject{}},
	}}}
	call := findFunctionCall(geminiConvertMessages(messages, "google", "gemini-3-pro", true))
	if call == nil || len(call.ID) != 64 {
		t.Fatalf("function call = %#v", call)
	}
}

func findFunctionCall(contents []geminiContent) *geminiFunctionCall {
	for _, content := range contents {
		for index := range content.Parts {
			if content.Parts[index].FunctionCall != nil {
				return content.Parts[index].FunctionCall
			}
		}
	}
	return nil
}

func findFunctionResponse(contents []geminiContent) *geminiFuncResponse {
	for _, content := range contents {
		for index := range content.Parts {
			if content.Parts[index].FunctionResponse != nil {
				return content.Parts[index].FunctionResponse
			}
		}
	}
	return nil
}
