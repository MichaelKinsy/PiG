package ai

import (
	"strings"
	"testing"
	"unsafe"
)

// The usual replayed assistant message holds one text block: its request content is that block's string itself, not a copy of it. Pi
// joins the parts (openai-completions.ts convertMessages: textParts.map((part) => part.text).join("")), which V8 does without copying
// one string either.
func TestAnAssistantMessageOfOneTextBlockIsConvertedWithoutCopyingItsText(t *testing.T) {
	text := strings.Repeat("an answer the model gave earlier in the conversation. ", 20)
	out, err := convertCompletionsMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: text}}}}, completionsConvertOptions{instructionRole: "system"})
	if err != nil || len(out) != 1 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	content, ok := out[0].Content.(string)
	if !ok || content != text {
		t.Fatalf("content = %#v, want the block's text", out[0].Content)
	}
	if unsafe.StringData(content) != unsafe.StringData(text) { //nolint:gosec // G103: the test compares the addresses of two strings.
		t.Error("the content is a copy of the block's text")
	}
}

func TestSeveralTextBlocksAreJoinedAndBlankOnesDropped(t *testing.T) {
	message := AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "one "}, TextContent{Text: " \n"}, TextContent{Text: "two"}, TextContent{Text: ""}, TextContent{Text: " three"}, TextContent{Text: "4"}, TextContent{Text: "5"}}}
	out, err := convertCompletionsMessages([]Message{message}, completionsConvertOptions{instructionRole: "system"})
	if err != nil || len(out) != 1 || out[0].Content != "one two three45" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	// Only blank text: the message has no content and, without tool calls, is dropped.
	out, err = convertCompletionsMessages([]Message{AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "  "}}}}, completionsConvertOptions{instructionRole: "system"})
	if err != nil || len(out) != 0 {
		t.Fatalf("a blank assistant message was kept: %+v, err = %v", out, err)
	}
	for _, test := range []struct {
		texts []string
		want  string
	}{{nil, ""}, {[]string{""}, ""}, {[]string{"", ""}, ""}, {[]string{"a"}, "a"}, {[]string{"a", "", "b"}, "ab"}} {
		if got := joinTexts(test.texts); got != test.want {
			t.Errorf("joinTexts(%q) = %q, want %q", test.texts, got, test.want)
		}
	}
}

// The messages that lead a request (the anchored tools of the first system message, then the system prompt) are written into room the
// conversion left ahead of the conversation, which is converted once into its place: nothing is copied to make room.
func TestTheLeadingMessagesAreWrittenAheadOfTheConversationInPlace(t *testing.T) {
	out, err := convertCompletionsMessages([]Message{UserMessage{Content: UserText("hello")}, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "hi"}}}}, completionsConvertOptions{instructionRole: "system", leadingSlots: 2})
	if err != nil || len(out) != 4 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if out[0].Role != "" || out[1].Role != "" || out[2].Role != "user" || out[3].Role != "assistant" {
		t.Fatalf("the conversation does not follow two empty leading messages: %+v", out)
	}
	p := NewOpenAIProvider(OpenAIConfig{ProviderID: "test", Model: "m", BaseURL: "http://127.0.0.1:1/v1"}).(*openAIProvider)
	transcript := NormalizeContext(Context{SystemPrompt: "be brief", Messages: []Message{UserMessage{Content: UserText("hello")}, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "hi"}}}, UserMessage{Content: UserText("again")}}})
	conversion, err := p.convertTranscript(transcript, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	chat := conversion.chat
	if len(chat) != 4 || chat[0].Role != "system" || chat[0].Content != "be brief" || chat[1].Role != "user" || chat[2].Role != "assistant" || chat[3].Content != "again" {
		t.Fatalf("chat = %+v", chat)
	}
	if cap(chat) != len(chat) {
		t.Errorf("the request's messages have spare room: len %d cap %d", len(chat), cap(chat))
	}
}
