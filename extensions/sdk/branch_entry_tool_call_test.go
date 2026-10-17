package sdk

import (
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi's assistant message carries its tool calls as ToolCall blocks whose arguments are an object (packages/ai/src/types.ts:423-431). Decoding that into a
// string field failed the whole block list, so an assistant message with a tool call lost its text, its thinking and its tool calls.
func TestBranchEntryKeepsAnAssistantMessageWithAPiToolCall(t *testing.T) {
	const entry = `{"id":"e2","parentId":"e1","type":"message","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hm "},{"type":"text","text":"let me look"},{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"a.txt","n":2}},{"type":"thinking","thinking":"again"},{"type":"toolCall","id":"c2","name":"ls","arguments":"{\"dir\":\".\"}"},{"type":"toolCall","id":"c3","name":"pwd","arguments":null},{"type":"text","text":" now"}],"provider":"p","model":"m"}}`
	var got BranchEntry
	if err := json.Unmarshal([]byte(entry), &got); err != nil {
		t.Fatal(err)
	}
	if got.Content != "let me look now" || got.Thinking != "hm again" {
		t.Errorf("content %q, thinking %q; want the text and thinking blocks around the tool calls", got.Content, got.Thinking)
	}
	want := []ToolCallInfo{{Name: "read", ID: "c1", Args: `{"path":"a.txt","n":2}`}, {Name: "ls", ID: "c2", Args: `{"dir":"."}`}, {Name: "pwd", ID: "c3", Args: ""}}
	if len(got.ToolCalls) != len(want) {
		t.Fatalf("tool calls = %+v, want %+v", got.ToolCalls, want)
	}
	for i := range want {
		if got.ToolCalls[i] != want[i] {
			t.Errorf("tool call %d = %+v, want %+v", i, got.ToolCalls[i], want[i])
		}
	}
	if got.Provider != "p" || got.ModelID != "m" {
		t.Errorf("provider/model = %q/%q", got.Provider, got.ModelID)
	}
}

// The same inputs as the Rust SDK's branch_entry_tests.rs: both typed views answer them alike.
func TestBranchEntryDecodeMatchesTheRustSDK(t *testing.T) {
	decode := func(raw string) (BranchEntry, bool) {
		var b BranchEntry
		err := json.Unmarshal([]byte(raw), &b)
		return b, err == nil
	}
	if _, ok := decode(`"assistant"`); ok {
		t.Error("a string entry decoded")
	}
	if _, ok := decode(`[1]`); ok {
		t.Error("an array entry decoded")
	}
	if _, ok := decode(`{"id":5,"type":"message"}`); ok {
		t.Error("a numeric id decoded")
	}
	if _, ok := decode(`{"type":"message","summary":5}`); ok {
		t.Error("a numeric summary decoded")
	}
	if b, ok := decode(`{"id":"e1","type":"message","message":{"role":5,"content":"x"}}`); !ok || b.ID != "e1" || b.Type != "message" || b.Role != "" || b.Content != "" {
		t.Errorf("numeric role: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","message":{"role":"assistant","provider":"p","content":[{"type":"text","text":5},{"type":"toolCall","id":"c","name":"n","arguments":{}}]}}`); !ok || b.Role != "assistant" || b.Provider != "p" || b.Content != "" || len(b.ToolCalls) != 0 {
		t.Errorf("a block list that does not decode: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","content":"flat","message":"nested?"}`); !ok || b.Content != "flat" {
		t.Errorf("a message that is not an object: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","message":{"role":"user","content":5}}`); !ok || b.Role != "user" || b.Content != "" {
		t.Errorf("numeric content: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"model_change","provider":"p","modelId":"m"}`); !ok || b.Type != "model_change" || b.Content != "" || b.ModelID != "" {
		t.Errorf("model_change: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"custom_message","customType":"x","content":"shown","summary":"ignored"}`); !ok || b.Content != "shown" {
		t.Errorf("custom_message: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","message":{"role":"user","content":[{"type":"tool_result","text":"t"},{"type":"text","text":"u"}]}}`); !ok || b.Content != "tu" {
		t.Errorf("tool_result and text blocks: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","message":{"role":"tool","isError":"yes","content":"x"}}`); !ok || b.Role != "" || b.Content != "" {
		t.Errorf("a non-boolean isError: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","message":{"role":"assistant","content":"x","usage":{"input":"many"}}}`); !ok || b.Role != "" || b.Content != "" || b.Usage != nil {
		t.Errorf("a usage that does not decode: %+v, %v", b, ok)
	}
	if b, ok := decode(`{"type":"message","message":{"role":"assistant","content":["stray",{"type":"text","text":"kept?"}]}}`); !ok || b.Role != "assistant" || b.Content != "" {
		t.Errorf("a stray non-object block: %+v, %v", b, ok)
	}
}

// Pi's custom_message content is `string | (TextContent | ImageContent)[]` (session-manager.ts CustomMessageEntry). Block content gives the text of its text
// blocks; decoding it into a string failed the entry, so the mirror dropped it from the branch. The same cases as the Rust SDK's
// a_custom_message_with_block_content_takes_its_text.
func TestBranchEntryCustomMessageWithBlockContentTakesItsText(t *testing.T) {
	for raw, want := range map[string]string{
		`{"id":"c1","type":"custom_message","customType":"x","content":[{"type":"text","text":"a"},{"type":"image","data":"AA==","mimeType":"image/png"},{"type":"text","text":"b"}],"display":true}`: "ab",
		`{"type":"custom_message","content":[{"type":"image","data":"AA==","mimeType":"image/png"}],"summary":"s"}`:                                                                                   "s",
		`{"type":"custom_message","content":null,"summary":"s"}`:                                                                                                                                      "s",
		`{"type":"message","message":{"role":"user","content":[null,{"type":"text","text":"kept"}]}}`:                                                                                                 "kept",
	} {
		var b BranchEntry
		if err := json.Unmarshal([]byte(raw), &b); err != nil || b.Content != want {
			t.Errorf("%s: content %q, err %v; want %q", raw, b.Content, err, want)
		}
	}
	for _, raw := range []string{`{"type":"custom_message","content":5}`, `{"type":"custom_message","content":{"text":"x"}}`} {
		var b BranchEntry
		if err := json.Unmarshal([]byte(raw), &b); err == nil {
			t.Errorf("%s decoded as %+v, want an error", raw, b)
		}
	}
}
