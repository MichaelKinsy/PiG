package ai

import (
	"strings"
	"testing"
)

func googleTerminalMessage(t *testing.T, events []AssistantMessageEvent) *AssistantMessage {
	t.Helper()
	for _, event := range events {
		switch event := event.(type) {
		case DoneEvent:
			return event.Message
		case ErrorEvent:
			return event.Error
		}
	}
	t.Fatal("Google stream has no terminal event")
	return nil
}

func TestGoogleSSE_ThinkingThoughtSignature(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"reasoning","thought":true,"thoughtSignature":"c2lnbmF0dXJl"}]}}]}

data: {"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}

`
	message := googleTerminalMessage(t, runGoogleSSE(t, sse))
	thinking, ok := message.Content[0].(ThinkingContent)
	if !ok || thinking.ThinkingSignature != "c2lnbmF0dXJl" {
		t.Fatalf("thinking block = %#v", message.Content[0])
	}
}

func TestGoogleSSE_ThinkingSignatureRetainsLastNonEmpty(t *testing.T) {
	sse := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"a","thought":true,"thoughtSignature":"c2lnMQ=="}]}}]}

data: {"candidates":[{"content":{"role":"model","parts":[{"text":"b","thought":true}]}}]}

data: {"candidates":[{"finishReason":"STOP"}]}

`
	message := googleTerminalMessage(t, runGoogleSSE(t, sse))
	thinking, ok := message.Content[0].(ThinkingContent)
	if !ok || thinking.Thinking != "ab" || thinking.ThinkingSignature != "c2lnMQ==" {
		t.Fatalf("thinking block = %#v", message.Content[0])
	}
}

func TestGoogleConvertMessages_ResendsThinkingSignature(t *testing.T) {
	messages := []Message{AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{ThinkingContent{Thinking: "reasoning", ThinkingSignature: "c2lnbmF0dXJl"}},
	}}
	part := findThinkingPart(t, geminiConvertMessages(messages, "google", "gemini-3-pro", true))
	if part.Text == nil || *part.Text != "reasoning" || part.ThoughtSignature != "c2lnbmF0dXJl" {
		t.Fatalf("thinking part = %#v", part)
	}
}

func TestGoogleConvertMessages_KeepsEmptySignedThinking(t *testing.T) {
	messages := []Message{AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{ThinkingContent{ThinkingSignature: "c2lnbmF0dXJl"}},
	}}
	part := findThinkingPart(t, geminiConvertMessages(messages, "google", "gemini-3-pro", true))
	if part.ThoughtSignature != "c2lnbmF0dXJl" {
		t.Fatalf("empty signed thinking signature = %q", part.ThoughtSignature)
	}
}

func TestGoogleConvertMessages_DropsEmptyUnsignedThinking(t *testing.T) {
	messages := []Message{AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{ThinkingContent{Thinking: "   "}},
	}}
	for _, content := range geminiConvertMessages(messages, "google", "gemini-3-pro", true) {
		for _, part := range content.Parts {
			if part.Thought != nil && *part.Thought {
				t.Fatalf("empty unsigned thinking was not dropped: %#v", part)
			}
		}
	}
}

func TestGoogleConvertMessages_DropsInvalidThinkingSignature(t *testing.T) {
	messages := []Message{AssistantMessage{
		Provider: "google", Model: "gemini-3-pro",
		Content: []AssistantContentBlock{ThinkingContent{Thinking: "reasoning", ThinkingSignature: "abc!"}},
	}}
	part := findThinkingPart(t, geminiConvertMessages(messages, "google", "gemini-3-pro", true))
	if strings.TrimSpace(part.ThoughtSignature) != "" {
		t.Fatalf("invalid thinking signature was sent: %q", part.ThoughtSignature)
	}
}

func findThinkingPart(t *testing.T, contents []geminiContent) geminiPart {
	t.Helper()
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.Thought != nil && *part.Thought {
				return part
			}
		}
	}
	t.Fatal("no thinking part produced")
	return geminiPart{}
}
