// Code generated from internal/jsstring/slice.go by tui/sdk_tui_copy_test.go; DO NOT EDIT.

package jsstring

import "unicode/utf8"

// Length counts JavaScript UTF-16 units in a UTF-8/WTF-8 string.
func Length(text string) int {
	count := 0
	for len(text) > 0 {
		if text[0] < utf8.RuneSelf {
			n := ASCIIPrefix(text)
			text = text[n:]
			count += n
			continue
		}
		if len(text) >= 3 && text[0] == 0xed && text[1] >= 0xa0 && text[1] <= 0xbf && text[2] >= 0x80 && text[2] <= 0xbf {
			text = text[3:]
			count++
			continue
		}
		r, size := utf8.DecodeRuneInString(text)
		text = text[size:]
		count++
		if r > 0xffff {
			count++
		}
	}
	return count
}

// ASCIIPrefix returns the length of text's leading run of ASCII bytes, one UTF-16 unit each. It tests eight bytes per step.
func ASCIIPrefix(text string) int {
	i := 0
	for ; i+8 <= len(text); i += 8 {
		word := text[i : i+8]
		if (word[0]|word[1]|word[2]|word[3]|word[4]|word[5]|word[6]|word[7])&0x80 != 0 {
			break
		}
	}
	for i < len(text) && text[i] < utf8.RuneSelf {
		i++
	}
	return i
}

// Slice applies String.slice's unit offsets, including negative offsets, an omitted end and retained surrogate halves.
func Slice(text string, start int, ends ...int) string {
	if length, ok := validUnits(text); ok {
		end := length
		if len(ends) > 0 {
			end = ends[0]
		}
		start, end = unitIndex(start, length), unitIndex(end, length)
		if from, ok := byteOffset(text, start); ok {
			if to, ok := byteOffset(text, max(start, end)); ok {
				return text[from:to]
			}
		}
	}
	return sliceUnits(text, start, ends...)
}

func sliceUnits(text string, start int, ends ...int) string {
	units := ToUTF16(text)
	end := len(units)
	if len(ends) > 0 {
		end = ends[0]
	}
	start, end = unitIndex(start, len(units)), unitIndex(end, len(units))
	return FromUTF16(units[start:max(start, end)])
}

// unitIndex resolves a String.slice offset against length.
func unitIndex(n, length int) int {
	if n < 0 {
		return max(0, length+n)
	}
	return min(n, length)
}

// validUnits counts the UTF-16 units of text and reports whether text is valid UTF-8, so that its byte offsets at code point boundaries are
// exactly the offsets of the JavaScript string. WTF-8 surrogate halves and invalid bytes are not valid UTF-8.
func validUnits(text string) (int, bool) {
	count := 0
	for i := 0; i < len(text); {
		if text[i] < utf8.RuneSelf {
			i++
			count++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && size == 1 {
			return 0, false
		}
		i += size
		count++
		if r > 0xffff {
			count++
		}
	}
	return count, true
}

// byteOffset returns the byte offset of UTF-16 unit n in valid UTF-8 text, and false when n falls between the two halves of a surrogate pair.
func byteOffset(text string, n int) (int, bool) {
	units := 0
	for i := 0; i < len(text); {
		if units >= n {
			return i, units == n
		}
		if text[i] < utf8.RuneSelf {
			i++
			units++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		units++
		if r > 0xffff {
			units++
		}
	}
	return len(text), units == n
}
