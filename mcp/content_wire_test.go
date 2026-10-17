package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/mcp"
)

// packages/mcp/src/protocol/content.ts:30 ResourceLinkContent: a resource_link block built in Go writes uri, name, title,
// description, mimeType and size, and a decoded block reads them back.
func TestResourceLinkBlockWritesEveryMember(t *testing.T) {
	size := 2048.0
	block := mcp.ContentBlock{Type: "resource_link", URI: "file:///a.txt", Name: "a.txt", Title: "A", Description: "the file",
		MimeType: "text/plain", Size: &size}
	data, err := json.Marshal(block)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"type":"resource_link","mimeType":"text/plain","uri":"file:///a.txt","name":"a.txt","title":"A","description":"the file","size":2048}`
	if string(data) != want {
		t.Fatalf("marshal = %s, want %s", data, want)
	}
	var decoded mcp.ContentBlock
	if err := json.Unmarshal([]byte(want), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Size == nil || *decoded.Size != size || decoded.Title != "A" || !decoded.HasTitle || decoded.Description != "the file" || decoded.URI != "file:///a.txt" {
		t.Fatalf("decoded = %+v", decoded)
	}
}
