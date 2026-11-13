package uiview

import (
	"strings"
	"testing"
)

// pig additive (D614): the bytes of an image are not text. A view carries them in its own `images` list, as base64 in
// each record's `data`, and a client's textBytes bounds the longest text a node shows. The registry therefore leaves
// that member out of the longest-text figure and keeps it in the snapshot's bytes. Before, a photograph of more than
// 49,152 bytes (65,536 base64 characters) was refused by every client that takes 65,536 bytes of text.
func TestImageBytesAreNotTheLongestText(t *testing.T) {
	a := richAbilities()
	a.Nodes = []string{"text", "box", "image"}
	a.Limits.TextBytes, a.Limits.SnapshotBytes = 65536, 262144
	// 94,212 base64 characters carry 70,657 bytes, the photograph of internal/photonimage/testdata.
	photo := strings.Repeat("A", 94212)
	view := func(data string) map[string]any {
		return map[string]any{
			"root":   map[string]any{"kind": "image", "ref": "r1", "mimeType": "image/jpeg"},
			"images": []any{map[string]any{"ref": "r1", "mimeType": "image/jpeg", "data": data}},
		}
	}
	limit := func(what string, err *Error, text string) {
		t.Helper()
		if err == nil || err.Code != CodeLimit || !strings.Contains(err.Message, text) {
			t.Errorf("%s: error = %v, want limit with %q", what, err, text)
		}
	}
	wantOK(t, "an image of 70,657 bytes", tryMount(t, a, mountRecord("k", map[string]any{"view": view(photo)})))
	limit("a text of the same length", tryMount(t, a, mountRecord("k", map[string]any{"view": map[string]any{"root": map[string]any{"kind": "text", "text": photo}}})),
		"longest text is 94212, the client takes 65536")
	// The bytes still count in the snapshot: 200,000 bytes are 266,668 base64 characters.
	limit("an image of 200,000 bytes", tryMount(t, a, mountRecord("k", map[string]any{"view": view(strings.Repeat("A", 266668))})),
		"snapshot bytes is ")
	// Only the view's own list is image bytes: a member of the same name on a node is text like any other.
	node := map[string]any{"root": map[string]any{"kind": "text", "text": "t", "images": []any{map[string]any{"data": photo}}}}
	limit("a node member named images", tryMount(t, a, mountRecord("k", map[string]any{"view": node})), "longest text is 94212, the client takes 65536")
	// A record's other members are text: a mime type of that length is refused.
	mime := view("AAAA")
	mime["images"].([]any)[0].(map[string]any)["mimeType"] = photo
	limit("an image record's mime type", tryMount(t, a, mountRecord("k", map[string]any{"view": mime})), "longest text is 94212, the client takes 65536")
}
