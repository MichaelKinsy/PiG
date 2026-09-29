package text

import (
	"unicode/utf16"
	"unicode/utf8"
)

// Ports packages/coding-agent/src/modes/rpc/jsonl.ts (JSON.stringify string encoding).
// QuoteUTF16 returns a JSON.stringify-compatible string token, preserving lone UTF-16 units as escapes instead of replacing them with U+FFFD. It does not apply HTML escaping.
func QuoteUTF16(units []uint16) []byte {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(units)+2)
	out = append(out, '"')
	for i := 0; i < len(units); i++ {
		unit := units[i]
		switch unit {
		case '"', '\\':
			out = append(out, '\\', byte(unit))
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			switch {
			case unit >= 0xd800 && unit <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff:
				out = utf8.AppendRune(out, utf16.DecodeRune(rune(unit), rune(units[i+1])))
				i++
			case unit < 0x20 || utf16.IsSurrogate(rune(unit)):
				out = append(out, '\\', 'u', hex[unit>>12], hex[unit>>8&0xf], hex[unit>>4&0xf], hex[unit&0xf])
			default:
				out = utf8.AppendRune(out, rune(unit))
			}
		}
	}
	return append(out, '"')
}
