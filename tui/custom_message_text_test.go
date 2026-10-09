package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi's CustomMessageComponent shows a string content as is and joins the text
// blocks of an array content with newlines, skipping other blocks. Content
// from an extension arrives JSON-decoded, as []any of map[string]any.
func TestCustomMessageTextMatchesPi(t *testing.T) {
	var decoded any
	if err := json.Unmarshal([]byte(`[{"type":"text","text":"No results.\n\nSources"},{"type":"image","data":"AAAA","mimeType":"image/png"},{"type":"text","text":"- None"}]`), &decoded); err != nil {
		t.Fatal(err)
	}
	type block struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	for name, tc := range map[string]struct {
		content any
		want    string
	}{
		"string":       {"plain *markdown*", "plain *markdown*"},
		"decoded JSON": {decoded, "No results.\n\nSources\n- None"},
		"typed maps":   {[]map[string]any{{"type": "text", "text": "a"}, {"type": "text", "text": "b"}}, "a\nb"},
		"typed blocks": {[]block{{Type: "text", Text: "a"}, {Type: "image"}, {Type: "text", Text: "b"}}, "a\nb"},
		"empty array":  {[]any{}, ""},
	} {
		if got := CustomMessageText(&CustomMessage{CustomType: "x", Content: tc.content}); got != tc.want {
			t.Errorf("%s: CustomMessageText = %q, want %q", name, got, tc.want)
		}
	}
}

// CustomMessageComponent extends Container (custom-message.ts:14): Spacer(1) and a Box(1, 1) holding a Text(label, 0, 0), a
// Spacer(1) and the Markdown body, so a label wider than the box wraps like any Text; no row is wider than the width.
func TestCustomMessageComponentIsAContainerWhoseLabelWrapsInsideTheBox(t *testing.T) {
	c := NewCustomMessageComponent(&CustomMessage{CustomType: "my-type", Content: "body"}, nil, nil, 1)
	if got := len(c.Children()); got != 2 {
		t.Fatalf("children = %d, want 2", got)
	}
	lines := c.Render(8)
	for i, line := range lines {
		if got := widthx.VisibleWidth(line); got > 8 {
			t.Fatalf("line %d is %d cells wide at width 8: %q", i, got, line)
		}
	}
	joined := stripANSI(strings.Join(lines, "\n"))
	if strings.Contains(joined, "[my-type]") || !strings.Contains(joined, "[my-ty") {
		t.Fatalf("label did not wrap inside the box:\n%s", joined)
	}
}
