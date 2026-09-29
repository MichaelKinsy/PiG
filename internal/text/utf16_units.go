package text

import (
	"unicode/utf16"
	"unicode/utf8"
)

// UTF16Units converts Go strings to JavaScript string units. WTF-8 carries lone surrogates without collapsing them into replacement characters; other malformed UTF-8 follows Go's replacement-character decoding.
func UTF16Units(value string) []uint16 {
	units := make([]uint16, 0, len(value))
	for i := 0; i < len(value); {
		if i+2 < len(value) && value[i] == 0xed && value[i+1] >= 0xa0 && value[i+1] <= 0xbf && value[i+2]&0xc0 == 0x80 {
			unit := uint16(value[i]&0x0f)<<12 | uint16(value[i+1]&0x3f)<<6 | uint16(value[i+2]&0x3f)
			units = append(units, unit)
			i += 3
			continue
		}
		code, size := utf8.DecodeRuneInString(value[i:])
		i += size
		if code <= 0xffff {
			units = append(units, uint16(code))
		} else {
			high, low := utf16.EncodeRune(code)
			units = append(units, uint16(high), uint16(low))
		}
	}
	return units
}
