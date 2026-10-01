package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/content.test.ts.

func TestToLLMContentPassesTextAndImagesThroughAndReplacesOtherBlocksWithText(t *testing.T) {
	var blocks []mcp.ContentBlock
	err := json.Unmarshal([]byte(`[
		{"type":"text","text":"hello","annotations":{"priority":1}},
		{"type":"image","data":"aW1n","mimeType":"image/png","_meta":{"x":1}},
		{"type":"audio","data":"YXVk","mimeType":"audio/wav"},
		{"type":"resource_link","uri":"file:///a.txt","name":"a.txt"},
		{"type":"resource","resource":{"uri":"file:///b.txt","text":"inline"}},
		{"type":"resource","resource":{"uri":"file:///c.png","mimeType":"image/png","blob":"Yw=="}},
		{"type":"resource","resource":{"uri":"file:///d.bin","blob":"ZA=="}}
	]`), &blocks)
	if err != nil {
		t.Fatal(err)
	}
	got := mcp.ToLLMContent(mcp.CallToolResult{Content: blocks})
	want := []mcp.LLMContent{
		{Type: "text", Text: "hello"},
		{Type: "image", Data: "aW1n", MimeType: "image/png"},
		{Type: "text", Text: "[audio audio/wav omitted]"},
		{Type: "text", Text: "a.txt: file:///a.txt"},
		{Type: "text", Text: "inline"},
		{Type: "image", Data: "Yw==", MimeType: "image/png"},
		{Type: "text", Text: "[binary resource file:///d.bin (unknown type) omitted]"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d blocks, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("block %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestToLLMContentFallsBackToTheStructuredContentAsJSONWhenThereAreNoBlocks(t *testing.T) {
	got := mcp.ToLLMContent(mcp.CallToolResult{Content: []mcp.ContentBlock{}, StructuredContent: json.RawMessage(`{"n":1}`)})
	if len(got) != 1 || got[0] != (mcp.LLMContent{Type: "text", Text: "{\n  \"n\": 1\n}"}) {
		t.Fatalf("got %#v", got)
	}
	got = mcp.ToLLMContent(mcp.CallToolResult{
		Content:           []mcp.ContentBlock{{Type: "text", Text: "n=1"}},
		StructuredContent: json.RawMessage(`{"n":1}`),
	})
	if len(got) != 1 || got[0] != (mcp.LLMContent{Type: "text", Text: "n=1"}) {
		t.Fatalf("got %#v", got)
	}
}
