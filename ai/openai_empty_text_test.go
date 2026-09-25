package ai

import (
	"encoding/json"
	"testing"
)

// Pi 0.87.1 openai-completions.ts filters user content parts with
// item.type !== "text" || item.text.length > 0 and skips a user message only
// when no part remains. Whitespace-only text has non-zero length and is kept.
func TestConvertMessagesDropsEmptyUserTextParts(t *testing.T) {
	messages := []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: ""}, ImageContent{MimeType: "image/png", Data: "AAAA"}}},
		UserMessage{Content: UserContentBlocks{TextContent{Text: ""}}},
		UserMessage{Content: UserContentBlocks{TextContent{Text: " "}}},
	}
	out, err := convertMessagesInternal(messages, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}}]},{"role":"user","content":[{"type":"text","text":" "}]}]`
	if string(got) != want {
		t.Fatalf("converted messages\n got %s\nwant %s", got, want)
	}
}
