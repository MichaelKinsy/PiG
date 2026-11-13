package tools

import (
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// StructuredContentMatches tells whether a structured content is the one a read result's content derives, as bytes.Equal against
// RebuildStructuredContent would, and without the copy that rebuilding makes.
func assertStructuredContentMatchesRebuild(t *testing.T, content []ai.ToolResultMessageContent) {
	t.Helper()
	want := readStructuredContent(content)
	if !StructuredContentMatches("read", content, want) {
		t.Fatalf("StructuredContentMatches(read, %#v) rejects the structured content readStructuredContent writes: %q", content, want)
	}
	for _, mutated := range mutatedStructuredContents(want) {
		if StructuredContentMatches("read", content, mutated) {
			t.Fatalf("StructuredContentMatches(read, %#v) accepts %q, not %q", content, mutated, want)
		}
	}
}

func mutatedStructuredContents(want []byte) [][]byte {
	out := [][]byte{nil, {}, []byte("x"), append(append([]byte(nil), want...), ' ')}
	if len(want) > 0 {
		out = append(out, want[:len(want)-1], want[1:])
		for _, at := range []int{0, len(want) / 2, len(want) - 1} {
			m := append([]byte(nil), want...)
			m[at] ^= 1
			out = append(out, m)
		}
	}
	return out
}

func TestStructuredContentMatchesReadStructuredContent(t *testing.T) {
	long := strings.Repeat("é€😀 \"quoted\" \\ \u2028 \x01 \xff line\n", 400)
	cases := [][]ai.ToolResultMessageContent{
		nil,
		{ai.TextContent{Text: ""}},
		{ai.TextContent{Text: "Line 1\nLine 2"}},
		{ai.TextContent{Text: "<a href=\"x\">&amp;</a> \u2028 \u2029 \x00\x01\x1f\x7f\b\f\n\r\t\\/"}},
		{ai.TextContent{Text: "\xff\xfe\xc3 \xe2\x82 \xed\xa0\x80 \xed\xa0\xbd\xed\xb8\x80 \ufffd é 😀"}},
		{ai.TextContent{Text: long}, ai.TextContent{Text: "second"}},
		{ai.TextContent{Text: strings.Repeat("😀", 5000)}},
		{ai.TextContent{Text: strings.Repeat("\xe2\x82", 5000)}},
		{ai.TextContent{Text: strings.Repeat("\x80", 5000)}},
		{ai.ImageContent{Data: "iVBORw0KGgo=", MimeType: "image/png"}, ai.TextContent{Text: "note <&>"}},
	}
	for _, content := range cases {
		assertStructuredContentMatchesRebuild(t, content)
	}
	// Cuts that fall inside and next to multi-byte sequences, whatever the chunk size.
	rng := rand.New(rand.NewPCG(20261111, 3))
	for range 3000 {
		var b strings.Builder
		for range rng.IntN(1500) {
			b.WriteString(structuredContentPieces[rng.IntN(len(structuredContentPieces))])
		}
		assertStructuredContentMatchesRebuild(t, []ai.ToolResultMessageContent{ai.TextContent{Text: b.String()}})
	}
}

// Only the read tool derives its structured content from its content.
func TestStructuredContentMatchesOnlyForRead(t *testing.T) {
	content := []ai.ToolResultMessageContent{ai.TextContent{Text: "text"}}
	if StructuredContentMatches("bash", content, readStructuredContent(content)) {
		t.Fatal("a bash result's structured content matched")
	}
}

// The comparison copies nothing: a card retaining a read result checks the structured content once per finished call.
func TestStructuredContentMatchesDoesNotAllocate(t *testing.T) {
	content := []ai.ToolResultMessageContent{ai.TextContent{Text: readBenchTypeScript()}}
	structured := readStructuredContent(content)
	if allocs := testing.AllocsPerRun(20, func() {
		if !StructuredContentMatches("read", content, structured) {
			t.Fatal("no match")
		}
	}); allocs != 0 {
		t.Fatalf("StructuredContentMatches allocates %v times on a %d-byte read result, want 0", allocs, len(structured))
	}
}

func BenchmarkStructuredContentMatches(b *testing.B) {
	content := []ai.ToolResultMessageContent{ai.TextContent{Text: readBenchTypeScript()}}
	structured := readStructuredContent(content)
	b.ReportAllocs()
	b.SetBytes(int64(len(structured)))
	for b.Loop() {
		StructuredContentMatches("read", content, structured)
	}
}
