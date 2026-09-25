package latex

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// visibleWidth mirrors pi's tui/src/utils.ts visibleWidth by delegating to the
// canonical widthx.VisibleWidth, so LaTeX layout and the TUI renderer agree on
// every row's width.
func visibleWidth(s string) int {
	return widthx.VisibleWidth(s)
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// isJSSpace reports the JavaScript regex \s / String.prototype.trim whitespace
// set (WhiteSpace + LineTerminator): the parser and the format helpers depend on
// matching it so trimming and whitespace collapse are byte-faithful to pi.
func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ',
		'\u00a0', '\u1680', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000', '\ufeff':
		return true
	}
	return r >= '\u2000' && r <= '\u200a'
}

func trimJS(s string) string      { return strings.TrimFunc(s, isJSSpace) }
func trimStartJS(s string) string { return strings.TrimLeftFunc(s, isJSSpace) }
func trimEndJS(s string) string   { return strings.TrimRightFunc(s, isJSSpace) }

// sliceRunesFrom drops the first n runes of s (n leading layout spaces), guarding
// short lines the way JS String.slice(n) yields "" past the end.
func sliceRunesFrom(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if n >= len(r) {
		return ""
	}
	return string(r[n:])
}

func indexOfRune(src []rune, sub string, from int) int {
	subR := []rune(sub)
	if len(subR) == 0 {
		return from
	}
	if from < 0 {
		from = 0
	}
	for i := from; i+len(subR) <= len(src); i++ {
		if runesHavePrefix(src[i:], subR) {
			return i
		}
	}
	return -1
}

func runesHavePrefix(src, prefix []rune) bool {
	if len(src) < len(prefix) {
		return false
	}
	for i := range prefix {
		if src[i] != prefix[i] {
			return false
		}
	}
	return true
}
