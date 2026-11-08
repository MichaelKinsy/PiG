// Package weburl normalizes untrusted URLs for terminal output.
package weburl

// Ports packages/coding-agent/src/utils/web-url.ts

import (
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// WebURL is web-url.ts webUrl: the normalized href of the http: or https: URL value names, and false for anything else. The shared launcher also opens
// files and custom protocols, so login only opens web URLs; normalizing percent-encodes control characters, which would otherwise end a terminal hyperlink early.
func WebURL(value string) (string, bool) {
	parsed, err := nodeurl.ParseHTTPHref(value)
	if err != nil {
		return "", false
	}
	return parsed.Href, true
}

// DisplayURL is web-url.ts displayUrl: value normalized as a web URL, else with control characters replaced, since an untrusted string printed as it is
// could end a hyperlink early or send its own escape sequences to the terminal.
func DisplayURL(value string) string {
	if href, ok := WebURL(value); ok {
		return href
	}
	return widthx.ReplaceControlCharacters(value)
}
