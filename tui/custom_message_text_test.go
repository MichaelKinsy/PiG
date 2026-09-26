package tui

import (
	"encoding/json"
	"testing"
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
