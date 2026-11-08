package runtime

// UTF16Length is JavaScript's `text.length`: the number of UTF-16 code units.
func UTF16Length(text string) int {
	n := 0
	for _, r := range text {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// UTF16Slice is JavaScript's `s.slice(0, n)`: at most n UTF-16 code units. A surrogate pair the limit falls inside is cut in
// two; a Go string has no form for the lone surrogate, so the character becomes U+FFFD.
func UTF16Slice(s string, n int) string {
	units := 0
	for i, r := range s {
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > n {
			if units < n {
				return s[:i] + "\uFFFD"
			}
			return s[:i]
		}
		units += width
	}
	return s
}
