package ai

import (
	"strings"
	"testing"
)

func TestGoogleSSE_ToolCallThoughtSignature(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"bash","args":{"command":"ls"}},"thoughtSignature":"c2lnbmF0dXJl"}]}}]}

data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

`
	message := googleTerminalMessage(t, runGoogleSSE(t, sse))
	var signature string
	for _, block := range message.Content {
		if tool, ok := block.(ToolCall); ok {
			signature = tool.ThoughtSignature
		}
	}
	if signature != "c2lnbmF0dXJl" {
		t.Fatalf("tool-call thought signature = %q", signature)
	}
}

func TestGoogleConvertMessages_ResendsToolCallSignature(t *testing.T) {
	messages := []Message{AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{ToolCall{
			ID: "t1", Name: "bash", Arguments: JsonObject{"command": "ls"}, ThoughtSignature: "c2lnbmF0dXJl",
		}},
	}}
	output := geminiConvertMessages(messages, "google", "gemini-3-pro", true)
	for _, part := range output[0].Parts {
		if part.FunctionCall != nil && part.ThoughtSignature != "c2lnbmF0dXJl" {
			t.Fatalf("thought signature = %q", part.ThoughtSignature)
		}
	}
}

func TestGoogleConvertMessages_DropsInvalidToolCallSignature(t *testing.T) {
	messages := []Message{AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{ToolCall{
			ID: "t1", Name: "bash", Arguments: JsonObject{"command": "ls"}, ThoughtSignature: "abc!",
		}},
	}}
	for _, content := range geminiConvertMessages(messages, "google", "gemini-3-pro", true) {
		for _, part := range content.Parts {
			if part.FunctionCall != nil && strings.TrimSpace(part.ThoughtSignature) != "" {
				t.Fatalf("invalid thought signature was sent: %q", part.ThoughtSignature)
			}
		}
	}
}
