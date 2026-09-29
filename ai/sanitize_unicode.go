package ai

import (
	"strings"
	"unicode/utf16"
)

// Ports packages/ai/src/utils/sanitize-unicode.ts

// sanitizeSurrogates removes unpaired UTF-16 code units carried as WTF-8 in a Go string. Valid UTF-8 stays unchanged; paired WTF-8 code units become their Unicode scalar.
func sanitizeSurrogates(s string) string {
	first := strings.IndexByte(s, 0xed)
	for first >= 0 {
		if _, ok := wtf8SurrogateAt(s, first); ok {
			break
		}
		next := strings.IndexByte(s[first+1:], 0xed)
		if next < 0 {
			return s
		}
		first += next + 1
	}
	if first < 0 {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	out.WriteString(s[:first])
	for i := first; i < len(s); {
		unit, ok := wtf8SurrogateAt(s, i)
		if !ok {
			out.WriteByte(s[i])
			i++
			continue
		}
		if unit < 0xdc00 {
			if low, ok := wtf8SurrogateAt(s, i+3); ok && low >= 0xdc00 {
				out.WriteRune(utf16.DecodeRune(unit, low))
				i += 6
				continue
			}
		}
		i += 3
	}
	return out.String()
}

func wtf8SurrogateAt(s string, i int) (rune, bool) {
	if i+2 >= len(s) || s[i] != 0xed || s[i+1] < 0xa0 || s[i+1] > 0xbf || s[i+2]&0xc0 != 0x80 {
		return 0, false
	}
	return 0xd000 | rune(s[i+1]&0x3f)<<6 | rune(s[i+2]&0x3f), true
}
