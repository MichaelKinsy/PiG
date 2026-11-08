package compaction

import (
	"encoding/json"
	"testing"
)

// A custom message's content blocks are held as the JSON text an extension wrote, to keep their member order; the token estimate counts them as it counts the
// decoded blocks.
func TestCustomContentCharsCountsContentHeldAsJSON(t *testing.T) {
	blocks := `[{"type":"text","text":"abc"},{"type":"image","mimeType":"image/png","data":"aGk="},{"type":"text","text":"de"}]`
	var decoded any
	if err := json.Unmarshal([]byte(blocks), &decoded); err != nil {
		t.Fatal(err)
	}
	want := 3 + estimatedImageChars + 2
	if got := customContentChars(decoded); got != want {
		t.Fatalf("decoded blocks: %d, want %d", got, want)
	}
	if got := customContentChars(json.RawMessage(blocks)); got != want {
		t.Errorf("blocks held as JSON text: %d, want %d", got, want)
	}
	if got := customContentChars(json.RawMessage(`not json`)); got != 0 {
		t.Errorf("invalid JSON text: %d, want 0", got)
	}
}
