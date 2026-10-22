// Code generated from internal/jsstring/codepoint.go by tui/sdk_tui_copy_test.go; DO NOT EDIT.

package jsstring

import (
	"unicode/utf16"
	"unicode/utf8"
)

// DecodeRuneInString reads a JavaScript code point from UTF-8/WTF-8. Adjacent encoded surrogate halves combine just as UTF-16 codePointAt does after strings are concatenated.
func DecodeRuneInString(text string) (r rune, size int) {
	// Match unicode/utf8's inlineable ASCII branch; uncommon decoding stays out of the caller's hot path.
	if text != "" && text[0] < utf8.RuneSelf {
		return rune(text[0]), 1
	} else {
		r, size = decodeNonASCII(text)
	}
	return
}

func decodeNonASCII(text string) (rune, int) {
	if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf && text[2] >= 0x80 && text[2] <= 0xbf {
		first := rune(text[0]&15)<<12 | rune(text[1]&63)<<6 | rune(text[2]&63)
		if first <= 0xdbff && len(text) >= 6 && text[3] == 0xed && text[4] >= 0xb0 && text[4] <= 0xbf && text[5] >= 0x80 && text[5] <= 0xbf {
			second := rune(text[3]&15)<<12 | rune(text[4]&63)<<6 | rune(text[5]&63)
			return utf16.DecodeRune(first, second), 6
		}
		return first, 3
	}
	return utf8.DecodeRuneInString(text)
}

// Splice joins the prefix before start, insert, and the suffix beginning at end, using String.slice's UTF-16 offset rules.
func Splice(text string, start, end int, insert string) string {
	if length, ok := validUnits(text); ok && utf8.ValidString(insert) {
		if from, ok := byteOffset(text, unitIndex(start, length)); ok {
			if to, ok := byteOffset(text, unitIndex(end, length)); ok {
				return text[:from] + insert + text[to:]
			}
		}
	}
	return spliceUnits(text, start, end, insert)
}

func spliceUnits(text string, start, end int, insert string) string {
	units := ToUTF16(text)
	inserted := ToUTF16(insert)
	start, end = unitIndex(start, len(units)), unitIndex(end, len(units))
	result := make([]uint16, 0, start+len(inserted)+len(units)-end)
	result = append(result, units[:start]...)
	result = append(result, inserted...)
	result = append(result, units[end:]...)
	return FromUTF16(result)
}
