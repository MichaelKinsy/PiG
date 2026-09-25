// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strconv"
	"strings"
	"unicode"
)

// Label and source-text handling, ported from grok-mermaid labels.ts.

const (
	wrapWidth    = 24 // node labels wrap to at most this many columns per line
	minWrapWidth = 4  // ...and no narrower when fitting a diagram to a width
	maxLines     = 4  // ...and at most this many lines; overflow truncates with …
	maxLabel     = 28 // edge labels are truncated to this many columns
)

// labelBreakChars: identifier-boundary characters preferred as break points so a
// too-wide word is not sliced mid-segment. Mirrors grok-build's TOKEN_BREAK_CHARS.
var labelBreakChars = []rune{'_', '-', '.', '/'}

// asciiLower/asciiUpper do ASCII-only case folding, matching Rust's
// to_ascii_lowercase/uppercase (Unicode folding could change string length).
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

func asciiUpper(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r - ('a' - 'A')
		}
		return r
	}, s)
}

// isControlStripped reports the C0/C1 controls stripControls removes (all but
// \t\n\r): 0x00-0x08, 0x0b, 0x0c, 0x0e-0x1f, 0x7f-0x9f.
func isControlStripped(r rune) bool {
	switch {
	case r >= 0x00 && r <= 0x08, r == 0x0b, r == 0x0c, r >= 0x0e && r <= 0x1f, r >= 0x7f && r <= 0x9f:
		return true
	}
	return false
}

// stripControls removes control characters that measure a column but paint none
// (or collide with sentinels / inject ANSI). Applied at every untrusted entry.
func stripControls(src string) string {
	return strings.Map(func(r rune) rune {
		if isControlStripped(r) {
			return -1
		}
		return r
	}, src)
}

// srcLines splits like Rust str::lines: on \n, trailing \r stripped, and without
// a final empty line when the input ends in a newline.
func srcLines(src string) []string {
	parts := strings.Split(src, "\n")
	out := make([]string, len(parts))
	for i, l := range parts {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// isAlphanumeric approximates Rust char::is_alphanumeric (Alphabetic | Numeric).
func isAlphanumeric(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// isIdChar reports characters allowed in a bare node/state/class identifier.
func isIdChar(r rune) bool { return isAlphanumeric(r) || r == '_' }

const entityLookahead = 10

var namedEntities = map[string]string{
	"lt": "<", "gt": ">", "amp": "&", "quot": "\"", "apos": "'",
}

func isHexDigits(s string) bool {
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'f'
		isUpper := r >= 'A' && r <= 'F'
		if !isDigit && !isLower && !isUpper {
			return false
		}
	}
	return s != ""
}

func isDecDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func decodeEntityBody(body string) (string, bool) {
	if named, ok := namedEntities[body]; ok {
		return named, true
	}
	if !strings.HasPrefix(body, "#") {
		return "", false
	}
	num := body[1:]
	hex := strings.HasPrefix(num, "x") || strings.HasPrefix(num, "X")
	digits := num
	if hex {
		digits = num[1:]
	}
	if hex {
		if !isHexDigits(digits) {
			return "", false
		}
	} else if !isDecDigits(digits) {
		return "", false
	}
	base := 10
	if hex {
		base = 16
	}
	code, err := strconv.ParseInt(digits, base, 64)
	if err != nil {
		return "", false
	}
	if code > 0x10ffff || (code >= 0xd800 && code <= 0xdfff) {
		return "", false
	}
	if code < 0x20 || (code >= 0x7f && code <= 0x9f) {
		return "", false
	}
	return string(rune(code)), true
}

// decodeHtmlEntities decodes HTML entities in label text in a single forward
// pass, so `&amp;lt;` decodes to the literal `&lt;` rather than to `<`.
func decodeHtmlEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}
	chars := []rune(s)
	var out strings.Builder
	i := 0
	for i < len(chars) {
		if chars[i] != '&' {
			out.WriteRune(chars[i])
			i++
			continue
		}
		hi := min(i+1+entityLookahead, len(chars))
		semi := -1
		for j := i + 1; j < hi; j++ {
			if chars[j] == ';' {
				semi = j
				break
			}
		}
		decoded, ok := "", false
		if semi != -1 {
			decoded, ok = decodeEntityBody(string(chars[i+1 : semi]))
		}
		if !ok {
			out.WriteByte('&')
			i++
		} else {
			out.WriteString(decoded)
			i = semi + 1
		}
	}
	return out.String()
}

// stripMarkdown removes markdown emphasis from a backtick label string, keeping
// `*`/`_` that sit inside a word so snake_case survives.
func stripMarkdown(s string) string {
	noCode := strings.ReplaceAll(s, "`", "")
	noStrong := strings.ReplaceAll(strings.ReplaceAll(noCode, "**", ""), "__", "")
	chars := []rune(noStrong)
	var out strings.Builder
	for i, c := range chars {
		inWord := i > 0 && isAlphanumeric(chars[i-1]) &&
			i+1 < len(chars) && isAlphanumeric(chars[i+1])
		if (c == '*' || c == '_') && !inWord {
			continue
		}
		out.WriteRune(c)
	}
	return strings.TrimFunc(out.String(), unicode.IsSpace)
}

var htmlFormatTags = map[string]bool{
	"b": true, "strong": true, "i": true, "em": true, "u": true, "s": true,
	"strike": true, "del": true, "ins": true, "mark": true, "small": true,
	"big": true, "sub": true, "sup": true, "code": true, "kbd": true, "samp": true,
	"var": true, "tt": true, "span": true, "font": true, "q": true, "abbr": true,
	"cite": true, "pre": true,
}

func isTagNameChar(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

// htmlTagAt reads a tag starting at start, returning its name and index after >.
func htmlTagAt(chars []rune, start int) (name string, end int, ok bool) {
	i := start + 1
	if i < len(chars) && chars[i] == '/' {
		i++
	}
	nameStart := i
	for i < len(chars) && isTagNameChar(chars[i]) {
		i++
	}
	if i == nameStart {
		return "", 0, false
	}
	name = string(chars[nameStart:i])
	for i < len(chars) && chars[i] != '>' {
		if chars[i] == '<' {
			return "", 0, false
		}
		i++
	}
	if i < len(chars) && chars[i] == '>' {
		return name, i + 1, true
	}
	return "", 0, false
}

func stripHtmlTags(s string) string {
	chars := []rune(s)
	var out strings.Builder
	i := 0
	for i < len(chars) {
		if chars[i] == '<' {
			if name, end, ok := htmlTagAt(chars, i); ok {
				lower := strings.ToLower(name)
				if lower == "br" {
					out.WriteByte(' ')
					i = end
					continue
				}
				if htmlFormatTags[lower] {
					i = end
					continue
				}
			}
		}
		out.WriteRune(chars[i])
		i++
	}
	return out.String()
}

// unwrap strips one matching pair of wrapping delimiters, if present.
func unwrap(s, open, close string) (string, bool) {
	if len(s) >= len(open)+len(close) && strings.HasPrefix(s, open) && strings.HasSuffix(s, close) {
		return s[len(open) : len(s)-len(close)], true
	}
	return "", false
}

// cleanLabel normalises raw label text: strip markup, unquote, decode entities.
func cleanLabel(raw string) string {
	trimmed := strings.TrimSpace(stripHtmlTags(strings.TrimSpace(raw)))
	unquoted := trimmed
	if u, ok := unwrap(trimmed, "\"", "\""); ok {
		unquoted = u
	} else if u, ok := unwrap(trimmed, "'", "'"); ok {
		unquoted = u
	}
	unquoted = strings.TrimSpace(unquoted)
	if md, ok := unwrap(unquoted, "`", "`"); ok {
		return decodeHtmlEntities(stripMarkdown(strings.TrimSpace(md)))
	}
	return decodeHtmlEntities(unquoted)
}

// lastBreak returns the index of the last identifier-boundary character, or -1.
func lastBreak(s string) int {
	best := -1
	for _, c := range labelBreakChars {
		if idx := strings.LastIndex(s, string(c)); idx > best {
			best = idx
		}
	}
	return best
}

// maxLinesFor returns the line budget for labels wrapped to wrap columns.
//
// At the natural width this is maxLines, matching grok-mermaid. Narrowing a
// diagram to fit an area (RenderWithin) reduces how much text a line holds, so
// the budget grows to keep the same total capacity: narrowing then costs rows
// rather than words. Without this, fitting a diagram silently truncated labels
// with an ellipsis, which is worse than the raw source it replaced, because the
// source at least still carries the whole label.
func maxLinesFor(wrap int) int {
	if wrap >= wrapWidth || wrap < 1 {
		return maxLines
	}
	budget := (wrapWidth*maxLines + wrap - 1) / wrap
	if budget < maxLines {
		return maxLines
	}
	return budget
}

// wrapLabel wraps a label to width columns over at most maxLines lines,
// truncating the last line with an ellipsis if it overflows.
func wrapLabel(label string, width, maxLn int) (lines []string, splitWord bool) {
	if width < 1 {
		width = 1
	}
	cur := ""
	curW := 0

	for word := range strings.FieldsSeq(label) {
		ww := stringWidth(word)
		switch {
		case ww > width:
			splitWord = true
			if cur != "" {
				lines = append(lines, cur)
			}
			chunk := ""
			chunkW := 0
			for _, mc := range measured(word) {
				if chunkW+mc.width > width && chunk != "" {
					p := lastBreak(chunk)
					carry := ""
					if p == -1 {
						lines = append(lines, chunk)
					} else {
						carry = chunk[p+1:]
						lines = append(lines, chunk[:p+1])
					}
					chunk = carry
					chunkW = stringWidth(carry)
				}
				chunk += mc.cluster
				chunkW += mc.width
			}
			cur = chunk
			curW = chunkW
		case cur == "":
			cur = word
			curW = ww
		case curW+1+ww <= width:
			cur += " " + word
			curW += 1 + ww
		default:
			lines = append(lines, cur)
			cur = word
			curW = ww
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}

	if len(lines) > maxLn {
		lines = lines[:maxLn]
		target := max(width-1, 1)
		var s strings.Builder
		sw := 0
		for _, mc := range measured(lines[len(lines)-1]) {
			if sw+mc.width > target {
				break
			}
			s.WriteString(mc.cluster)
			sw += mc.width
		}
		lines[len(lines)-1] = s.String() + "…"
	}
	return lines, splitWord
}

// fitLabel truncates to inner columns, leaving room for the ellipsis.
func fitLabel(label string, inner int) string {
	if stringWidth(label) <= inner {
		return label
	}
	var out strings.Builder
	used := 0
	for _, mc := range measured(label) {
		if used+mc.width+1 > inner {
			break
		}
		out.WriteString(mc.cluster)
		used += mc.width
	}
	return out.String() + "…"
}
