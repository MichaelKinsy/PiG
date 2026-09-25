package tui

import (
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

type graphemeSegment struct {
	Text  string
	Start int
	End   int
	Width int
}

// pig divergence (D27): byte-for-byte upstream PUNCTUATION_REGEX set (excludes _),
// used by grapheme-class word navigation.
const punctuationChars = "(){}[]<>.,;:'\"!?+-=*/\\|&%^$#@~`"

// grapheme-aware: editor cursor movement, deletion, wrapping, and width must
// operate on user-perceived characters, not individual runes. Mirrors
// upstream editor.ts use of Intl.Segmenter.
func graphemeSegments(s string) []graphemeSegment {
	if s == "" {
		return nil
	}
	var segs []graphemeSegment
	from := 0
	for rest := s; rest != ""; {
		var g string
		g, rest = widthx.FirstGrapheme(rest)
		segs = append(segs, graphemeSegment{Text: g, Start: from, End: from + len(g), Width: widthx.GraphemeWidth(g)})
		from += len(g)
	}
	return segs
}

// grapheme-aware: walk by grapheme boundaries instead of rune boundaries.
func previousGraphemeStart(s string, col int) int {
	if col <= 0 {
		return 0
	}
	segs := graphemeSegments(s[:col])
	if len(segs) == 0 {
		return 0
	}
	return segs[len(segs)-1].Start
}

// grapheme-aware: walk by grapheme boundaries instead of rune boundaries.
func nextGraphemeEnd(s string, col int) int {
	if col >= len(s) {
		return len(s)
	}
	segs := graphemeSegments(s[col:])
	if len(segs) == 0 {
		return len(s)
	}
	return col + segs[0].End
}

func graphemeAt(s string, col int) graphemeSegment {
	for _, seg := range graphemeSegments(s) {
		if col >= seg.Start && col < seg.End {
			return seg
		}
	}
	return graphemeSegment{Start: len(s), End: len(s), Width: 1}
}

func isWhitespaceGrapheme(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func isPunctuationGrapheme(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(punctuationChars, r) {
			return false
		}
	}
	return true
}

// grapheme-aware: display-column to byte-offset translation must advance by
// grapheme widths so ZWJ emoji consume one visual unit.
func byteOffsetForColumnGrapheme(s string, targetCol int) int {
	if targetCol <= 0 {
		return 0
	}
	cols := 0
	for _, seg := range graphemeSegments(s) {
		if cols+seg.Width > targetCol {
			return seg.Start
		}
		cols += seg.Width
		if cols >= targetCol {
			return seg.End
		}
	}
	return len(s)
}
