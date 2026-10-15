package codemode

import (
	"encoding/json"
	"testing"
)

// types.ts:47 CodemodeOutputItem is { type: "text"; text } | { type: "image"; data; mimeType }: "type" is the closed union text | image and serializes as that literal.
func TestOutputItemTypesAreTheUpstreamLiterals(t *testing.T) {
	for item, want := range map[OutputItem]string{
		{Type: OutputItemText, Text: "x"}:                         `{"type":"text","text":"x"}`,
		{Type: OutputItemImage, Data: "d", MimeType: "image/png"}: `{"type":"image","data":"d","mimeType":"image/png"}`,
	} {
		raw, err := json.Marshal(item)
		if err != nil || string(raw) != want {
			t.Errorf("marshal %+v = %s (%v), want %s", item, raw, err, want)
		}
	}
}
