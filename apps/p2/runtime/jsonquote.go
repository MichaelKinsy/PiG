package runtime

import "encoding/json"

// jsonQuote is `JSON.stringify(text)` for a string: UTF-8 text with the characters JSON requires escaped. (Go's encoder
// also escapes <, > and &, which JSON.stringify does not; the quoted text only feeds an error message.)
func jsonQuote(text string) string {
	quoted, _ := json.Marshal(text)
	return string(quoted)
}
