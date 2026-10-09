// Ports packages/tui/src/utils.ts
package utf16units

func truncate(s string, maxLen int) string {
	if len(s) > maxLen { // want `len of a string counts bytes`
		return s[:maxLen] // want `string slice by byte offsets`
	}
	return s
}

func empty(s string) bool { return len(s) == 0 }

func cursor(s string, pos int) byte {
	return s[pos] // want `string index yields a byte`
}

func good(s string) string { return s[:0] }
