package sdk

import (
	"encoding/json"
	"testing"
)

// A ToolResult with images uses upstream's block array, keeps text unchanged,
// and carries terminate; a text-only result keeps string content.
func TestToolResultWireShape(t *testing.T) {
	data, err := json.Marshal(ToolResult{Content: "  padded  ", Images: []ImageContent{{Data: "aW1n", MimeType: "image/png"}}, Terminate: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"content":[{"type":"text","text":"  padded  "},{"type":"image","data":"aW1n","mimeType":"image/png"}],"terminate":true}`
	if string(data) != want {
		t.Fatalf("wire = %s\nwant  %s", data, want)
	}
	data, err = json.Marshal(ToolResult{Content: "plain", IsError: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"content":"plain","is_error":true}`; string(data) != want {
		t.Fatalf("wire = %s, want %s", data, want)
	}
}
