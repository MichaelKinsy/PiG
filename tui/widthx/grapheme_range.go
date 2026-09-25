package widthx

import (
	"regexp"
	"strings"
)

// osc8HyperlinkPattern matches a complete OSC 8 hyperlink escape (as returned by
// ExtractAnsiCode); group 1 is the URL. Mirrors upstream's inline regex in
// getOsc8LinkAtColumn.
var osc8HyperlinkPattern = regexp.MustCompile("^\x1b\\]8;[^;]*;([^\x07\x1b]*)(?:\x07|\x1b\\\\)$")

// StripTerminalSequences removes ANSI/OSC/APC escapes from s, leaving only
// printable content with the upstream-compatible layout extractor.
func StripTerminalSequences(s string) string {
	if !strings.ContainsRune(s, 0x1B) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if _, n := ExtractAnsiCode(s, i); n > 0 {
			i += n
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// GetOsc8LinkAtColumn returns the OSC 8 hyperlink URL covering visible column
// `column` in line, or ok=false when no active link covers it. Mirrors upstream
// getOsc8LinkAtColumn, including its tab-as-3-cells width rule.
func GetOsc8LinkAtColumn(line string, column int) (url string, ok bool) {
	activeURL := ""
	hasActive := false
	currentCol := 0
	i := 0
	for i < len(line) {
		if code, n := ExtractAnsiCode(line, i); n > 0 {
			if m := osc8HyperlinkPattern.FindStringSubmatch(code); m != nil {
				if m[1] != "" {
					activeURL = m[1]
					hasActive = true
				} else {
					activeURL = ""
					hasActive = false
				}
			}
			i += n
			continue
		}
		textEnd := i
		for textEnd < len(line) {
			if _, n := ExtractAnsiCode(line, textEnd); n > 0 {
				break
			}
			textEnd++
		}
		g := newGraphemeIter(line[i:textEnd])
		for g.Next() {
			seg := g.Str()
			width := normalizeGraphemeWidth(seg, g.Width())
			if seg == "\t" {
				width = 3
			}
			if column >= currentCol && column < currentCol+width {
				if !hasActive {
					return "", false
				}
				return activeURL, true
			}
			currentCol += width
		}
		i = textEnd
	}
	return "", false
}

// This file ports the two upstream utils.ts helpers the layout engine needs
// (extractAnsiCode returning the matched code, and getGraphemeCellRange) plus the
// shared grapheme-width normalization they and VisibleWidth all require.
//
// Mirrors: .upstream/current/packages/tui/src/utils.ts
//   - extractAnsiCode  (returns {code, length})
//   - getGraphemeCellRange
//
// ExtractAnsi (width) and ExtractAnsiCode (layout) are the same upstream
// extractAnsiCode, so width and column accounting agree with Pi byte for byte.

// ExtractAnsiCode returns the ANSI escape beginning at byte offset pos in s
// (the matched substring and its byte length) or ("", 0) when none begins there.
// Mirrors upstream extractAnsiCode: CSI ends at m|G|K|H|J; OSC/APC end at BEL or
// ST (ESC \).
func ExtractAnsiCode(s string, pos int) (code string, length int) {
	if pos >= len(s) || s[pos] != 0x1B {
		return "", 0
	}
	if pos+1 >= len(s) {
		return "", 0
	}
	switch s[pos+1] {
	case '[':
		for j := pos + 2; j < len(s); j++ {
			c := s[j]
			if c == 'm' || c == 'G' || c == 'K' || c == 'H' || c == 'J' {
				return s[pos : j+1], j + 1 - pos
			}
		}
		return "", 0
	case ']', '_':
		for j := pos + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return s[pos : j+1], j + 1 - pos
			}
			if s[j] == 0x1B && j+1 < len(s) && s[j+1] == '\\' {
				return s[pos : j+2], j + 2 - pos
			}
		}
		return "", 0
	}
	return "", 0
}

// GraphemeCellRange reports the [start, end) cell-column span of the grapheme
// occupying display column `column` in line, or ok=false if no grapheme covers
// it (past the end of visible content). ANSI escapes are skipped and contribute
// no width. Mirrors upstream getGraphemeCellRange.
func GraphemeCellRange(line string, column int) (start, end int, ok bool) {
	currentCol := 0
	i := 0
	for i < len(line) {
		if _, n := ExtractAnsiCode(line, i); n > 0 {
			i += n
			continue
		}
		textEnd := i
		for textEnd < len(line) {
			if _, n := ExtractAnsiCode(line, textEnd); n > 0 {
				break
			}
			textEnd++
		}
		g := newGraphemeIter(line[i:textEnd])
		for g.Next() {
			width := normalizeGraphemeWidth(g.Str(), g.Width())
			if width > 0 && column >= currentCol && column < currentCol+width {
				return currentCol, currentCol + width, true
			}
			currentCol += width
		}
		i = textEnd
	}
	return 0, 0, false
}

// normalizeGraphemeWidth returns upstream graphemeWidth(seg). The iterator
// already computed it, so the second argument is returned as is.
func normalizeGraphemeWidth(_ string, width int) int { return width }
