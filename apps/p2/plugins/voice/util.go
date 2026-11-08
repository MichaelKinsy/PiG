package voice

import (
	"strings"
	"unicode/utf16"
)

// encodeURIComponent is JavaScript's: everything but letters, digits and `-_.!~*'()` is percent-escaped, as UTF-8.
func encodeURIComponent(text string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for _, b := range []byte(text) {
		switch {
		case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9', strings.IndexByte("-_.!~*'()", b) >= 0:
			out.WriteByte(b)
		default:
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		}
	}
	return out.String()
}

// truncateUTF16 is `text.slice(0, units)`: the first units UTF-16 code units, a split surrogate pair dropped.
func truncateUTF16(text string, units int) string {
	encoded := utf16.Encode([]rune(text))
	if len(encoded) <= units {
		return text
	}
	encoded = encoded[:units]
	if last := encoded[len(encoded)-1]; last >= 0xD800 && last < 0xDC00 {
		encoded = encoded[:len(encoded)-1]
	}
	return string(utf16.Decode(encoded))
}
