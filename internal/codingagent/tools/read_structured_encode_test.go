package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// encoderReadStructuredContent is readStructuredContent as it was before it wrote into one buffer of the exact size: a json.Encoder with
// HTML escaping off and its newline trimmed. The tests below hold the new encoder to its bytes.
func encoderReadStructuredContent(content []ai.ToolResultMessageContent) json.RawMessage {
	var text string
	var image *ai.ImageContent
	haveText := false
	for _, block := range content {
		switch block := block.(type) {
		case ai.TextContent:
			if !haveText {
				text, haveText = block.Text, true
			}
		case ai.ImageContent:
			if image == nil {
				image = &block
			}
		}
	}
	var out any = text
	if image != nil {
		out = struct {
			Type     string `json:"type"`
			Data     string `json:"data"`
			MimeType string `json:"mimeType"`
			Note     string `json:"note"`
		}{"image", image.Data, image.MimeType, text}
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(out)
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n"))
}

// structuredContentPieces are the byte classes the encoder branches on: characters it writes as they are (HTML characters included, as
// escaping is off), short escapes, \u00XX controls, U+2028 and U+2029, and ill-formed UTF-8 (a lone surrogate's WTF-8, a cut sequence).
var structuredContentPieces = []string{
	"a", "Z", " ", "<", ">", "&", "'", "/", "\x7f", "\"", "\\", "\b", "\f", "\n", "\r", "\t", "\x00", "\x01", "\x1f", "\x20",
	"é", "€", "😀", "\u2028", "\u2029", "\ufffd", "\u0080", "\u07ff", "\uffff", "\U0010ffff",
	"\xff", "\xfe", "\x80", "\xc3", "\xe2\x80", "\xf0\x9f\x98", "\xed\xa0\x80", "\xed\xb8\x80", "\xed\xa0\xbd\xed\xb8\x80", "\xc0\xaf",
	"export const x = 1;\n", `"\u2028"`,
}

func randomStructuredString(rng *rand.Rand) string {
	var b strings.Builder
	for range rng.IntN(24) {
		b.WriteString(structuredContentPieces[rng.IntN(len(structuredContentPieces))])
	}
	return b.String()
}

func assertStructuredContentMatchesEncoder(t *testing.T, content []ai.ToolResultMessageContent) {
	t.Helper()
	got, want := readStructuredContent(content), encoderReadStructuredContent(content)
	if !bytes.Equal(got, want) {
		t.Fatalf("readStructuredContent(%#v):\n got  %q\n want %q", content, got, want)
	}
}

// The structured content is what a json.Encoder with HTML escaping off writes, for text, images, both and neither, the first text and
// image winning.
func TestReadStructuredContentMatchesEncoder(t *testing.T) {
	cases := [][]ai.ToolResultMessageContent{
		nil,
		{},
		{ai.TextContent{Text: ""}},
		{ai.TextContent{Text: "Line 1\nLine 2\nLine 3"}},
		{ai.TextContent{Text: "<a href=\"x\">&amp;</a> \u2028 \u2029 \x00\x01\x1f\x7f\b\f\n\r\t\\/"}},
		{ai.TextContent{Text: "\xff\xfe\xc3 \xe2\x82 \xed\xa0\x80 \xed\xa0\xbd\xed\xb8\x80 \ufffd é 😀"}},
		{ai.TextContent{Text: "first"}, ai.TextContent{Text: "second"}},
		{ai.ImageContent{Data: "iVBORw0KGgo=", MimeType: "image/png"}},
		{ai.TextContent{Text: "Read image file [image/png]"}, ai.ImageContent{Data: "iVBORw0KGgo=", MimeType: "image/png"}},
		{ai.ImageContent{Data: "a+b/c==", MimeType: "image/jpeg"}, ai.TextContent{Text: "note <&>"}, ai.ImageContent{Data: "second", MimeType: "image/gif"}},
		{ai.ImageContent{}, ai.TextContent{Text: "\u2028"}},
		{ai.ImageContent{Data: "\"\\\n\xff\u2029", MimeType: "image/<x>"}, ai.TextContent{Text: "\x01"}},
	}
	for _, content := range cases {
		assertStructuredContentMatchesEncoder(t, content)
	}
	rng := rand.New(rand.NewPCG(20261013, 5))
	for range 20000 {
		var content []ai.ToolResultMessageContent
		for range rng.IntN(4) {
			if rng.IntN(3) == 0 {
				content = append(content, ai.ImageContent{Data: randomStructuredString(rng), MimeType: randomStructuredString(rng)})
			} else {
				content = append(content, ai.TextContent{Text: randomStructuredString(rng)})
			}
		}
		assertStructuredContentMatchesEncoder(t, content)
	}
}

func FuzzReadStructuredContentMatchesEncoder(f *testing.F) {
	for i, piece := range structuredContentPieces {
		f.Add(piece, structuredContentPieces[(i+1)%len(structuredContentPieces)], "image/png", i%2 == 0)
	}
	f.Fuzz(func(t *testing.T, text, data, mimeType string, withImage bool) {
		content := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
		if withImage {
			content = append(content, ai.ImageContent{Data: data, MimeType: mimeType})
		}
		assertStructuredContentMatchesEncoder(t, content)
	})
}

// readBenchTypeScript is a TypeScript file of about 140 KB with quotes, backslashes, tabs and HTML characters on its lines, more than read
// returns in one call.
func readBenchTypeScript() string {
	var b strings.Builder
	for i := 0; b.Len() < 140<<10; i++ {
		fmt.Fprintf(&b, "export function render%d(items: Array<Item>, label: string): string {\n\tconst sep = \"\\n\";\n\treturn items.map((item) => `<li data-id=\"${item.id}\">${label} & ${item.name}</li>`).join(sep);\n}\n\n", i)
	}
	return b.String()
}

func readBenchTool(tb testing.TB) *ReadTool {
	tb.Helper()
	dir := tb.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "render.ts"), []byte(readBenchTypeScript()), 0o600); err != nil {
		tb.Fatal(err)
	}
	return &ReadTool{CWD: dir}
}

// The structured content of a read is one allocation of its exact size: no encoder, no growing buffer to copy out of, and no boxed value.
func TestReadStructuredContentAllocatesOnlyItsBytes(t *testing.T) {
	tool := readBenchTool(t)
	result, err := tool.Execute(t.Context(), "id", json.RawMessage(`{"path":"render.ts"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("read: %+v, %v", result, err)
	}
	image := []ai.ToolResultMessageContent{ai.TextContent{Text: "Read image file [image/png]"}, ai.ImageContent{Data: strings.Repeat("iVBORw0KGgo+/", 4000), MimeType: "image/png"}}
	for name, content := range map[string][]ai.ToolResultMessageContent{"text": result.Content, "image": image} {
		if allocs := testing.AllocsPerRun(20, func() { _ = readStructuredContent(content) }); allocs > 1 {
			t.Errorf("%s structured content: %v allocations, want 1", name, allocs)
		}
		if got := readStructuredContent(content); cap(got) != len(got) {
			t.Errorf("%s structured content: capacity %d for %d bytes, want the exact size", name, cap(got), len(got))
		}
	}
}

// BenchmarkReadToolExecute is one read of a 140 KB TypeScript file: the first 50 KB of its text and that text's structured content.
func BenchmarkReadToolExecute(b *testing.B) {
	tool := readBenchTool(b)
	args := json.RawMessage(`{"path":"render.ts"}`)
	b.ReportAllocs()
	for b.Loop() {
		if result, err := tool.Execute(b.Context(), "id", args, nil); err != nil || result.IsError {
			b.Fatal(err, result.Content)
		}
	}
}
