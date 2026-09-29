package jsstring

import "unicode/utf8"

// Length counts JavaScript UTF-16 units in a UTF-8/WTF-8 string.
func Length(text string) int {
	count := 0
	for len(text) > 0 {
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

// Slice applies String.slice's unit offsets, including negative offsets, an omitted end and retained surrogate halves.
func Slice(text string, start int, ends ...int) string {
	units := ToUTF16(text)
	end := len(units)
	if len(ends) > 0 {
		end = ends[0]
	}
	index := func(n int) int {
		if n < 0 {
			return max(0, len(units)+n)
		}
		return min(n, len(units))
	}
	start, end = index(start), index(end)
	return FromUTF16(units[start:max(start, end)])
}
