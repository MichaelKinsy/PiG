// Package jsstring preserves JavaScript UTF-16 units at Go string boundaries. Lone surrogate units use WTF-8; ordinary Unicode strings retain their normal UTF-8 encoding.
package jsstring

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Canonical preserves UTF-16 units while giving paired surrogates their ordinary UTF-8 spelling.
func Canonical(text string) string {
	if utf8.ValidString(text) {
		return text
	}
	return FromUTF16(ToUTF16(text))
}

// FromUTF16 converts UTF-16 without replacing unpaired surrogates.
func FromUTF16(units []uint16) string {
	out := make([]byte, 0, len(units))
	for i := 0; i < len(units); i++ {
		r := rune(units[i])
		if r >= 0xd800 && r <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			r = utf16.DecodeRune(r, rune(units[i+1]))
			i++
		} else if r >= 0xd800 && r <= 0xdfff {
			out = append(out, byte(0xe0|(r>>12)), byte(0x80|((r>>6)&0x3f)), byte(0x80|(r&0x3f)))
			continue
		}
		out = utf8.AppendRune(out, r)
	}
	return string(out)
}

// ToUTF16 converts UTF-8/WTF-8 to the original JavaScript code units.
func ToUTF16(text string) []uint16 {
	out := make([]uint16, 0, len(text))
	for len(text) > 0 {
		if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf && text[2] >= 0x80 && text[2] <= 0xbf {
			out = append(out, uint16(text[0]&15)<<12|uint16(text[1]&63)<<6|uint16(text[2]&63))
			text = text[3:]
			continue
		}
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		if r > 0xffff {
			a, b := utf16.EncodeRune(r)
			out = append(out, uint16(a), uint16(b))
		} else {
			out = append(out, uint16(r))
		}
	}
	return out
}

// ToUTF8 replaces lone UTF-16 surrogates, matching a JavaScript string's UTF-8 encoding for operating-system byte APIs.
func ToUTF8(text string) []byte {
	return []byte(string(utf16.Decode(ToUTF16(text))))
}
