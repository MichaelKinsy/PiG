// Package text ports upstream packages/coding-agent/src/utils/text.ts: the
// leading byte order mark helpers every Pi reader applies before parsing
// user-authored JSON, markdown, and text files.
package text

import "strings"

// bom is the UTF-8 byte order mark as decoded text (U+FEFF).
const bom = "\xef\xbb\xbf"

// SplitBom splits a leading UTF-8 byte order mark from decoded text. It
// returns the mark ("" when absent) and the remaining text. Mirrors upstream
// splitBom.
func SplitBom(content string) (mark, text string) {
	if strings.HasPrefix(content, bom) {
		return bom, content[len(bom):]
	}
	return "", content
}

// StripBom removes a leading UTF-8 byte order mark from decoded text.
// Mirrors upstream stripBom.
func StripBom(content string) string {
	_, text := SplitBom(content)
	return text
}

// StripBomBytes is StripBom for file contents read as bytes, so JSON readers
// can strip the mark before decoding without a string round trip.
func StripBomBytes(content []byte) []byte {
	if len(content) >= len(bom) && string(content[:len(bom)]) == bom {
		return content[len(bom):]
	}
	return content
}
