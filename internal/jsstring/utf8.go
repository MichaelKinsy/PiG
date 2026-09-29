package jsstring

import "golang.org/x/text/encoding/unicode"

// FromUTF8 decodes operating-system bytes like Buffer.toString("utf8") and napi_create_string_utf8: each maximal ill-formed subpart becomes one replacement character, and a leading BOM is retained.
func FromUTF8(data []byte) string {
	// The UTF-8 decoder replaces malformed input; it has no rejecting input sequence.
	text, _ := unicode.UTF8.NewDecoder().String(string(data))
	return text
}
