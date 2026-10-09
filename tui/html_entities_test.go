package tui

import (
	"strings"
	"testing"
)

// packages/coding-agent/src/utils/html.ts:9-60 (decodeHtmlEntity, decodeHtmlEntityAt).
func TestDecodeHTMLEntity(t *testing.T) {
	for _, tc := range []struct {
		entity string
		want   string
		ok     bool
	}{
		{"amp", "&", true}, {"lt", "<", true}, {"gt", ">", true}, {"quot", `"`, true}, {"apos", "'", true},
		{"AMP", "", false}, {"nbsp", "", false}, {"", "", false},
		{"#65", "A", true}, {"#x41", "A", true}, {"#X41", "A", true}, {"#x1F600", "😀", true}, {"#x1f600", "😀", true}, {"#xfa", "\u00fa", true},
		{"#0", "\x00", true},
		// Number.parseInt reads the longest numeric prefix, an optional sign and (radix 16) a 0x prefix, and skips leading whitespace.
		{"#65abc", "A", true}, {"#+65", "A", true}, {"# 65", "A", true}, {"#x0x41", "A", true}, {"#xZ", "", false}, {"#x", "", false},
		{"#", "", false}, {"#-1", "", false}, {"#abc", "", false}, {"#1e3", "\x01", true},
		// String.fromCodePoint rejects code points above U+10FFFF; a surrogate stays a lone UTF-16 unit.
		{"#1114111", "\U0010ffff", true}, {"#1114112", "", false}, {"#x110000", "", false}, {"#99999999999999999999", "", false},
		{"#55296", "\xed\xa0\x80", true},
	} {
		got, ok := decodeHTMLEntity(tc.entity)
		if got != tc.want || ok != tc.ok {
			t.Errorf("decodeHTMLEntity(%q) = %q, %v; want %q, %v", tc.entity, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDecodeHTMLEntityAt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		html  string
		index int
		text  string
		size  int
		ok    bool
	}{
		{"named", "a&amp;b", 1, "&", 5, true},
		{"numeric", "&#x41;", 0, "A", 6, true},
		{"unknown name", "&nbsp;", 0, "", 0, false},
		{"no terminator", "&amp b", 0, "", 0, false},
		{"the next semicolon ends the entity", "&am p;&amp;", 0, "", 0, false},
		// :52 the semicolon may sit at most 16 UTF-16 units past the ampersand.
		{"semicolon 16 past the ampersand", "&#x" + strings.Repeat("0", 11) + "41;", 0, "A", 17, true},
		{"semicolon 15 past the ampersand", "&#x" + strings.Repeat("0", 10) + "41;", 0, "A", 16, true},
		{"semicolon 17 past the ampersand", "&#x" + strings.Repeat("0", 12) + "41;", 0, "", 0, false},
		// Units, not bytes: six supplementary characters put the semicolon 16 units (28 bytes) past the ampersand, a seventh makes it 18.
		{"astral characters count as two units", "&#65" + strings.Repeat("\U0001d7ce", 6) + ";", 0, "A", 29, true},
		{"astral characters beyond the limit", "&#65" + strings.Repeat("\U0001d7ce", 7) + ";", 0, "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, size, ok := decodeHTMLEntityAt(tc.html, tc.index)
			if text != tc.text || size != tc.size || ok != tc.ok {
				t.Fatalf("got %q, %d, %v; want %q, %d, %v", text, size, ok, tc.text, tc.size, tc.ok)
			}
		})
	}
}
