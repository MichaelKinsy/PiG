package tools

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// largeStructuredTexts are texts past the request cache's smallest entry, each class the three escapers (the tool's encoding/json form, the
// session's and the request's JSON.stringify form) treat alike or differently.
func largeStructuredTexts() map[string]string {
	pad := func(s string) string { return s + strings.Repeat("x", 4<<10) }
	return map[string]string{
		"ascii":          strings.Repeat("a line of the file with \"quotes\" and a \\ backslash\n", 200),
		"controls":       pad("\x00\x01\b\t\n\f\r\x1f\x7f"),
		"html":           pad("<a href=\"x\">&amp;</a>"),
		"line-separator": pad("a\u2028b\u2029c"),
		"astral":         pad("😀 é € \U0010ffff \ufffd"),
		"invalid":        pad("\xff\xfe\xc3 \xe2\x82 \xed\xa0\x80 \xed\xa0\xbd\xed\xb8\x80"),
	}
}

// A large text's structured content is the bytes the tool always wrote for it, whether it is the encoding the request cache keeps or the tool's own.
func TestReadStructuredContentOfALargeTextMatchesEncoder(t *testing.T) {
	for name, text := range largeStructuredTexts() {
		t.Run(name, func(t *testing.T) {
			assertStructuredContentMatchesEncoder(t, []ai.ToolResultMessageContent{ai.TextContent{Text: text}})
			assertStructuredContentMatchesEncoder(t, []ai.ToolResultMessageContent{ai.ImageContent{Data: "iVBORw0KGgo=", MimeType: "image/png"}, ai.TextContent{Text: text}})
		})
	}
}

// The structured content of a read result shares the encoding the session entry and the request use, so the text is escaped once: asking for
// it again, as the card does, finds the encoding already made, and what StructuredContentMatches compares is that encoding.
func TestReadStructuredContentSharesTheEncodingOfTheText(t *testing.T) {
	text := largeStructuredTexts()["ascii"]
	content := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
	first := readStructuredContent(content)
	var second []byte
	if allocs := testing.AllocsPerRun(20, func() { second = readStructuredContent(content) }); allocs != 0 {
		t.Fatalf("a second structured content of the same text allocates %v times, want 0", allocs)
	}
	if &first[0] != &second[0] {
		t.Fatal("the second structured content of the same text is another buffer")
	}
	if cap(first) != len(first) {
		t.Fatalf("the shared encoding can be appended to in place: cap %d, len %d", cap(first), len(first))
	}
	if !StructuredContentMatches("read", content, first) {
		t.Fatal("the structured content a read returned does not match its content")
	}
	if allocs := testing.AllocsPerRun(20, func() { StructuredContentMatches("read", content, first) }); allocs != 0 {
		t.Fatalf("matching a shared structured content allocates %v times, want 0", allocs)
	}
	// An encoding that is not the text's is not a match, shared or not.
	mutated := append([]byte(nil), first...)
	mutated[len(mutated)/2] ^= 1
	if StructuredContentMatches("read", content, mutated) {
		t.Fatal("a changed structured content matched")
	}
}

// A text with a character the two forms write differently (U+2028, or a byte that is not UTF-8) keeps the tool's own form and shares nothing.
func TestReadStructuredContentOfATextTheFormsWriteDifferentlyIsTheToolsOwn(t *testing.T) {
	for _, name := range []string{"line-separator", "invalid"} {
		text := largeStructuredTexts()[name]
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
		got := readStructuredContent(content)
		if want := encoderReadStructuredContent(content); string(got) != string(want) {
			t.Fatalf("%s: structured content differs from the encoder's", name)
		}
		if !StructuredContentMatches("read", content, got) {
			t.Fatalf("%s: the tool's own structured content does not match", name)
		}
	}
}
