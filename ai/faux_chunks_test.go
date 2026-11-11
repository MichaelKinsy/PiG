package ai

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// upstream: packages/ai/src/providers/faux.ts splitStringByTokenSize cuts a JavaScript string, so a chunk holds whole characters and never a fragment of one. A Go string is UTF-8, so a cut at a byte offset can fall inside a character, and a chunk that ends inside one is not valid text: the tool call arguments it extends show a broken character until the next chunk completes it.
func TestSplitByTokenSizeKeepsEveryCharacterWhole(t *testing.T) {
	// One, two, three and four byte characters, and a lone UTF-16 surrogate in its WTF-8 form (a JavaScript string can carry one).
	for _, text := range []string{
		strings.Repeat("ab🦀é-", 500),
		strings.Repeat("日本語", 700),
		strings.Repeat("x\xed\xa0\xbdy", 300),
	} {
		for size := 1; size <= 5; size++ {
			chunks := splitByTokenSize(text, size, size)
			var joined strings.Builder
			for index, chunk := range chunks {
				joined.WriteString(chunk)
				if !isWTF8(chunk) || !utf8.RuneStart(chunk[0]) {
					t.Fatalf("token size %d: chunk %d of %q... is %q, which holds part of a character", size, index, text[:12], chunk)
				}
			}
			if joined.String() != text {
				t.Fatalf("token size %d: the chunks do not join back into the text", size)
			}
		}
	}
}

// upstream: packages/ai/src/providers/faux.ts splitStringByTokenSize slices `tokenSize * 4` UTF-16 units. A character outside the Basic Multilingual Plane counts 2 and is never split, so a chunk that would end between the halves of one runs a unit over.
func TestSplitByTokenSizeCountsUTF16Units(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("é", 400),
		strings.Repeat("日本語", 200),
		strings.Repeat("🦀", 200),
		strings.Repeat("ab🦀é-", 100),
	} {
		for size := 1; size <= 5; size++ {
			chunks := splitByTokenSize(text, size, size)
			for index, chunk := range chunks[:len(chunks)-1] {
				if units := jsstring.Length(chunk); units != size*4 && units != size*4+1 {
					t.Fatalf("token size %d: chunk %d of %q... is %d UTF-16 units, want %d (or %d across a surrogate pair)", size, index, text[:8], units, size*4, size*4+1)
				}
			}
		}
	}
}
