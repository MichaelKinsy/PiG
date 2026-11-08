package sdk

import (
	"encoding/json"
	"slices"
	"testing"
)

// Pi's registerMarkdownTransformer(fn, { messageTypes }): no options select the default surfaces, a list is carried as given, and an empty list stays an
// empty list on the wire (the host reads it as "nowhere", not as the default).
func TestMarkdownTransformerMessageTypesOnTheRegisterFrame(t *testing.T) {
	transformer := func(markdown string, _ MarkdownTransformContext) string { return markdown }
	var none Extension
	if none.markdownMessageTypeList() != nil {
		t.Fatal("a frame without a transformer carries a surface list")
	}
	for _, tc := range []struct {
		name    string
		options []MarkdownTransformerOptions
		want    string
	}{
		{"default", nil, `["user","assistant","assistant-thinking"]`},
		{"options without a list", []MarkdownTransformerOptions{{}}, `["user","assistant","assistant-thinking"]`},
		{"listed", []MarkdownTransformerOptions{{MessageTypes: []string{"custom", "summary"}}}, `["custom","summary"]`},
		{"empty", []MarkdownTransformerOptions{{MessageTypes: []string{}}}, `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e Extension
			e.MarkdownTransformer(transformer, tc.options...)
			list := e.markdownMessageTypeList()
			if list == nil {
				t.Fatal("no list")
			}
			wire, err := json.Marshal(list)
			if err != nil || !slices.Equal([]byte(tc.want), wire) {
				t.Fatalf("list %s, %v; want %s", wire, err, tc.want)
			}
		})
	}
}
