package runtime

// JSTrim is `text.trim()`: JavaScript's white space and line terminators removed from both ends.
func JSTrim(text string) string { return jsTrim(text) }

// JSLength is `text.length`: the UTF-16 code units of text.
func JSLength(text string) int {
	count := 0
	for _, character := range text {
		count++
		if character >= 0x10000 {
			count++
		}
	}
	return count
}
