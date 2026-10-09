// Package jsdiff ports the parts of the npm `diff` package (jsdiff 8.0.4) that Pi calls: diffWords, which Pi's renderDiff uses
// for intra-line highlighting. The word diff is Myers' algorithm over whitespace-attached tokens, then a whitespace tidy pass.
package jsdiff

import (
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Change is one jsdiff change object: a run of text kept, added or removed.
type Change struct {
	Value   string
	Count   int
	Added   bool
	Removed bool
}

type component struct {
	count    int
	added    bool
	removed  bool
	previous *component
	value    string
}

type path struct {
	oldPos int
	last   *component
}

// hasJSSpace is /\s/.test(s).
func hasJSSpace(s string) bool {
	for _, r := range s {
		if jsstring.IsSpace(r) {
			return true
		}
	}
	return false
}

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return jsstring.Trim(s) }

// isExtendedWordChar is the character class jsdiff's word tokenizer counts as a word character (word.js extendedWordChars).
func isExtendedWordChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == 0xAD:
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2C6, r >= 0x2C8 && r <= 0x2D7, r >= 0x2DE && r <= 0x2FF:
		return true
	case r >= 0x1E00 && r <= 0x1EFF:
		return true
	}
	return false
}

// splitParts is value.match(/[ext]+|\s+|[^ext]/gu).
func splitParts(value string) []string {
	var parts []string
	for i := 0; i < len(value); {
		start := i
		r, size := utf8.DecodeRuneInString(value[i:])
		switch {
		case jsstring.IsSpace(r):
			for i < len(value) {
				r, size = utf8.DecodeRuneInString(value[i:])
				if !jsstring.IsSpace(r) {
					break
				}
				i += size
			}
		case isExtendedWordChar(r):
			for i < len(value) {
				r, size = utf8.DecodeRuneInString(value[i:])
				if !isExtendedWordChar(r) {
					break
				}
				i += size
			}
		default:
			i += size
		}
		parts = append(parts, value[start:i])
	}
	return parts
}

// tokenizeWords is WordDiff.tokenize without a segmenter: whitespace runs attach to the neighbouring word or punctuation token.
func tokenizeWords(value string) []string {
	var tokens []string
	prev, havePrev := "", false
	for _, part := range splitParts(value) {
		switch {
		case hasJSSpace(part):
			if !havePrev {
				tokens = append(tokens, part)
			} else {
				tokens[len(tokens)-1] += part
			}
		case havePrev && hasJSSpace(prev):
			if tokens[len(tokens)-1] == prev {
				tokens[len(tokens)-1] += part
			} else {
				tokens = append(tokens, prev+part)
			}
		default:
			tokens = append(tokens, part)
		}
		prev, havePrev = part, true
	}
	return tokens
}

// joinWords is WordDiff.join: the leading whitespace of every token but the first is dropped.
func joinWords(tokens []string) string {
	var b strings.Builder
	for i, token := range tokens {
		if i > 0 {
			token = strings.TrimLeftFunc(token, jsstring.IsSpace)
		}
		b.WriteString(token)
	}
	return b.String()
}

func wordsEqual(left, right string) bool { return jsTrim(left) == jsTrim(right) }

func removeEmpty(tokens []string) []string {
	out := tokens[:0:0]
	for _, token := range tokens {
		if token != "" {
			out = append(out, token)
		}
	}
	return out
}

func addToPath(p *path, added, removed bool, oldPosInc int) *path {
	last := p.last
	if last != nil && last.added == added && last.removed == removed {
		return &path{oldPos: p.oldPos + oldPosInc, last: &component{count: last.count + 1, added: added, removed: removed, previous: last.previous}}
	}
	return &path{oldPos: p.oldPos + oldPosInc, last: &component{count: 1, added: added, removed: removed, previous: last}}
}

func extractCommon(base *path, newTokens, oldTokens []string, diagonal int, equals func(a, b string) bool) int {
	newLen, oldLen := len(newTokens), len(oldTokens)
	oldPos := base.oldPos
	newPos := oldPos - diagonal
	common := 0
	for newPos+1 < newLen && oldPos+1 < oldLen && equals(oldTokens[oldPos+1], newTokens[newPos+1]) {
		newPos++
		oldPos++
		common++
	}
	if common > 0 {
		base.last = &component{count: common, previous: base.last}
	}
	base.oldPos = oldPos
	return newPos
}

func buildValues(last *component, newTokens, oldTokens []string, join func([]string) string) []*component {
	var components []*component
	for c := last; c != nil; c = c.previous {
		components = append(components, c)
	}
	for l, r := 0, len(components)-1; l < r; l, r = l+1, r-1 {
		components[l], components[r] = components[r], components[l]
	}
	newPos, oldPos := 0, 0
	for _, c := range components {
		if !c.removed {
			c.value = join(newTokens[newPos : newPos+c.count])
			newPos += c.count
			if !c.added {
				oldPos += c.count
			}
		} else {
			c.value = join(oldTokens[oldPos : oldPos+c.count])
			oldPos += c.count
		}
	}
	return components
}

// myers is Diff.diffWithOptionsObj: the shortest edit path between the token lists, preferring removals on ties. equals compares two
// tokens and join concatenates the tokens of one change into its value.
func myers(oldTokens, newTokens []string, equals func(a, b string) bool, join func([]string) string) []*component {
	newLen, oldLen := len(newTokens), len(oldTokens)
	editLength := 1
	maxEditLength := newLen + oldLen
	bestPath := map[int]*path{0: {oldPos: -1}}
	newPos := extractCommon(bestPath[0], newTokens, oldTokens, 0, equals)
	if bestPath[0].oldPos+1 >= oldLen && newPos+1 >= newLen {
		return buildValues(bestPath[0].last, newTokens, oldTokens, join)
	}
	const unbounded = int(^uint(0) >> 1)
	minDiagonal, maxDiagonal := -unbounded, unbounded
	for editLength <= maxEditLength {
		for diagonal := max(minDiagonal, -editLength); diagonal <= min(maxDiagonal, editLength); diagonal += 2 {
			removePath, addPath := bestPath[diagonal-1], bestPath[diagonal+1]
			if removePath != nil {
				bestPath[diagonal-1] = nil
			}
			canAdd := false
			if addPath != nil {
				addPathNewPos := addPath.oldPos - diagonal
				canAdd = 0 <= addPathNewPos && addPathNewPos < newLen
			}
			canRemove := removePath != nil && removePath.oldPos+1 < oldLen
			if !canAdd && !canRemove {
				bestPath[diagonal] = nil
				continue
			}
			var base *path
			if !canRemove || (canAdd && removePath.oldPos < addPath.oldPos) {
				base = addToPath(addPath, true, false, 0)
			} else {
				base = addToPath(removePath, false, true, 1)
			}
			newPos = extractCommon(base, newTokens, oldTokens, diagonal, equals)
			if base.oldPos+1 >= oldLen && newPos+1 >= newLen {
				return buildValues(base.last, newTokens, oldTokens, join)
			}
			bestPath[diagonal] = base
			if base.oldPos+1 >= oldLen {
				maxDiagonal = min(maxDiagonal, diagonal-1)
			}
			if newPos+1 >= newLen {
				minDiagonal = max(minDiagonal, diagonal+1)
			}
		}
		editLength++
	}
	return nil
}

func toChanges(components []*component) []Change {
	changes := make([]Change, len(components))
	for i, c := range components {
		changes[i] = Change{Value: c.value, Count: c.count, Added: c.added, Removed: c.removed}
	}
	return changes
}

// DiffWords is jsdiff's diffWords(oldStr, newStr) with default options.
func DiffWords(oldStr, newStr string) []Change {
	oldTokens := removeEmpty(tokenizeWords(oldStr))
	newTokens := removeEmpty(tokenizeWords(newStr))
	components := myers(oldTokens, newTokens, wordsEqual, joinWords)
	postProcess(components)
	return toChanges(components)
}

// tokenizeLines is the line tokenizer of jsdiff's diffLines: each token keeps its terminating "\n" (so a "\r\n" stays whole), and the
// last token has none when the text does not end in a newline.
func tokenizeLines(text string) []string {
	var tokens []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			tokens = append(tokens, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		tokens = append(tokens, text[start:])
	}
	return tokens
}

// DiffLines is jsdiff's diffLines(oldStr, newStr) with default options: Myers over line tokens compared exactly.
func DiffLines(oldStr, newStr string) []Change {
	return toChanges(myers(tokenizeLines(oldStr), tokenizeLines(newStr), func(a, b string) bool { return a == b }, func(tokens []string) string { return strings.Join(tokens, "") }))
}
