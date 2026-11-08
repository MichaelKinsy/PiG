package runtime

import "github.com/MichaelKinsy/PiG/internal/chord/chordjson"

// jsonQuote is `JSON.stringify(text)` for a string.
func jsonQuote(text string) string {
	quoted, _ := chordjson.Marshal(text)
	return string(quoted)
}
