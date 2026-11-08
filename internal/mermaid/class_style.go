// SPDX-FileCopyrightText: Copyright 2023-2026 SpaceXAI
// SPDX-FileCopyrightText: Copyright 2026 Alexey Zaytsev
// SPDX-License-Identifier: Apache-2.0 AND MIT

package mermaid

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Best-effort interpretation of `classDef` styles for a cell grid, ported from
// lovely-mermaid class-style.ts. A terminal cell can express a foreground, a
// background and boldness; fill is the node background, stroke its border,
// color its text, and every other property is ignored.

// ClassStyle is the terminal-expressible subset of a classDef; colors are
// `#rrggbb`. A nil field is absent.
type ClassStyle struct {
	Fill   *string
	Stroke *string
	Color  *string
	Bold   *bool
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}

func allHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isHexDigit(s[i]) {
			return false
		}
	}
	return true
}

// skipJSSpace returns the index after any JS whitespace at s[i:].
func skipJSSpace(s string, i int) int {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		if !jsstring.IsSpace(r) {
			break
		}
		i += n
	}
	return i
}

// readDigits reads one or more ASCII digits at s[i:].
func readDigits(s string, i int) (digits string, next int, ok bool) {
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	return s[i:j], j, j > i
}

func hex255(digits string) string {
	trimmed := strings.TrimLeft(digits, "0")
	n := 255
	if len(trimmed) <= 3 {
		if v, err := strconv.Atoi("0" + trimmed); err == nil && v < 255 {
			n = v
		}
	}
	h := strconv.FormatInt(int64(n), 16)
	if len(h) < 2 {
		h = "0" + h
	}
	return h
}

// normalizeColor maps `#rgb`, `#rrggbb`, `rgb(r,g,b)` or a CSS color name to
// `#rrggbb`; ok=false otherwise.
func normalizeColor(v string) (string, bool) {
	s := strings.ToLower(trimJS(v))
	if len(s) == 7 && s[0] == '#' && allHex(s[1:]) {
		return s, true
	}
	if len(s) == 4 && s[0] == '#' && allHex(s[1:]) {
		return "#" + s[1:2] + s[1:2] + s[2:3] + s[2:3] + s[3:4] + s[3:4], true
	}
	if rest, ok := strings.CutPrefix(s, "rgba("); ok {
		return rgbCall(rest)
	}
	if rest, ok := strings.CutPrefix(s, "rgb("); ok {
		return rgbCall(rest)
	}
	c, ok := namedColors[s]
	return c, ok
}

// rgbCall reads `\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)` after the open paren.
func rgbCall(rest string) (string, bool) {
	i := skipJSSpace(rest, 0)
	var parts [3]string
	for k := range parts {
		d, next, ok := readDigits(rest, i)
		if !ok {
			return "", false
		}
		parts[k] = d
		i = skipJSSpace(rest, next)
		if k < 2 {
			if i >= len(rest) || rest[i] != ',' {
				return "", false
			}
			i = skipJSSpace(rest, i+1)
		}
	}
	return "#" + hex255(parts[0]) + hex255(parts[1]) + hex255(parts[2]), true
}

// ResolveClassStyle is the merged style of a span's classes (later classes
// win), or nil when nothing terminal-expressible was declared.
func ResolveClassStyle(classes []string, classDefs map[string]map[string]string) *ClassStyle {
	if classes == nil {
		return nil
	}
	var out ClassStyle
	found := false
	for _, name := range classes {
		props, ok := classDefs[name]
		if !ok {
			continue
		}
		for k, v := range props {
			switch k {
			case "fill", "stroke", "color":
				c, ok := normalizeColor(v)
				if !ok {
					continue
				}
				switch k {
				case "fill":
					out.Fill = &c
				case "stroke":
					out.Stroke = &c
				default:
					out.Color = &c
				}
				found = true
			case "font-weight":
				t := trimJS(v)
				b := t == "bold" || t == "bolder"
				out.Bold = &b
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	return &out
}

// ContrastOn is black or white, whichever reads on the given `#rrggbb`
// background.
func ContrastOn(fill string) string {
	ch := func(i int) int {
		n, _ := strconv.ParseInt(fill[i:i+2], 16, 0)
		return int(n)
	}
	yiq := float64(ch(1)*299+ch(3)*587+ch(5)*114) / 1000
	if yiq >= 128 {
		return "#000000"
	}
	return "#ffffff"
}
