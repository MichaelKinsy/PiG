package ai

import (
	"encoding/json"
	"testing"
)

// openai-completions.ts:1268-1276 drops a user text part whose text is empty and then sanitizes the rest, so a part that is only a lone surrogate stays,
// with `"text":""`, where an empty part is dropped. The member is present on the wire.
func TestCompletionsUserTextThatSanitizesToNothingKeepsItsEmptyTextMember(t *testing.T) {
	lone := "\xed\xa0\xbd"
	model := &Model{ID: "m", ProviderMeta: ProviderMetadata{API: APIOpenAICompletions, ProviderID: "p"}}
	transcript := NormalizeContext(Context{Messages: []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: lone}, TextContent{Text: ""}, TextContent{Text: "kept"}}},
	}})
	out, err := ConvertCompletionsMessages(model, transcript, &OpenAICompat{}, ConvertCompletionsMessagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"role":"user","content":[{"type":"text","text":""},{"type":"text","text":"kept"}]}]`; string(wire) != want {
		t.Fatalf("messages = %s, want %s", wire, want)
	}
}
