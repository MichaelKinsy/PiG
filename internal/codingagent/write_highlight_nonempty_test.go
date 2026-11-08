package codingagent

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// The pieces a line of a file is joined from: code, white space, characters outside ASCII, HTML character references and
// the bytes of a lone surrogate in WTF-8, which highlighting must keep one by one.
var nonEmptyLinePieces = []string{
	"a", "Z", "0", " ", "  ", "\t", "(", ")", "{", "}", "<", ">", "&", "&amp;", "&#10;", "&lt;", "\"", "'", "`", "/", "//", "/*", "*/", "#", ";", ":", "=>",
	"\u00e9", "\u4e2d", "\U0001f600", "\u2028", "\u00a0", "\ufeff", "\xed\xa0\x80", "\xed\xb0\x80", "function", "return", "const", "def", "<div>", "</div>",
	"\\", "$", "@", "%", "-", "--", "```", "***", "__", "[", "]", "|",
}

// TestHighlightedLineOfTextIsNotEmpty: lineCount does not highlight a pending line that has text, because the highlighted
// line of a non-empty line is never empty, for every language the paths map to, one highlight.js does not know, and none.
func TestHighlightedLineOfTextIsNotEmpty(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 11))
	for _, lang := range []string{"typescript", "javascript", "python", "go", "rust", "ruby", "java", "c", "cpp", "csharp", "php", "bash", "sql", "html", "xml", "css", "scss", "json", "yaml", "toml", "markdown", "dockerfile", "makefile", "lua", "perl", "diff", "", "nosuchlanguage"} {
		for range 400 {
			var line strings.Builder
			for range 1 + random.IntN(12) {
				line.WriteString(nonEmptyLinePieces[random.IntN(len(nonEmptyLinePieces))])
			}
			text := normalizeWriteText(line.String())
			if text == "" || strings.Contains(text, "\n") {
				continue
			}
			if got := highlightSingleLine(text, lang); got == "" {
				t.Fatalf("%q highlights to an empty line in %q", text, lang)
			}
		}
	}
}

// TestWriteHighlightLineCountSkipsNoEmptyLine: lineCount of a streamed file, which highlights no pending line that has text,
// is the number of lines left when the empty highlighted lines at the end are dropped.
func TestWriteHighlightLineCountSkipsNoEmptyLine(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 5))
	for range 60 {
		var content strings.Builder
		for range 5 + random.IntN(80) {
			switch random.IntN(5) {
			case 0:
				content.WriteString("\n")
			case 1:
				content.WriteString("   \n")
			default:
				content.WriteString("const x = f(" + nonEmptyLinePieces[random.IntN(len(nonEmptyLinePieces))] + ");\n")
			}
		}
		text := content.String()
		var counted, drawn *writeHighlightCache
		for end := 0; end < len(text); {
			end = min(len(text), end+1+random.IntN(40))
			for end < len(text) && text[end]&0xc0 == 0x80 {
				end++
			}
			counted = updateWriteHighlightCacheIncremental(counted, "sample.ts", text[:end])
			drawn = updateWriteHighlightCacheIncremental(drawn, "sample.ts", text[:end])
			lines := drawn.lines()
			want := len(lines)
			for want > 0 && lines[want-1] == "" {
				want--
			}
			if got := counted.lineCount(); got != want {
				t.Fatalf("after %d bytes: lineCount %d, want %d", end, got, want)
			}
		}
	}
}
