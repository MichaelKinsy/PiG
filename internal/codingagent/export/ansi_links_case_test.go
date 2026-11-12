package export

import "testing"

// The scheme check of an OSC 8 link is Pi's /^(?:https?:|mailto:)/i. A JS /i regexp without the u flag folds a letter by
// toUpperCase and never maps a non-ASCII letter onto an ASCII one, so the long s U+017F is not an s there, while Go's (?i)
// folds it onto s: the pattern spells the case out instead.
func TestAnsiToHTMLSchemeCaseFoldsOnlyASCIILetters(t *testing.T) {
	const st = "\x1b\\"
	for _, tc := range []struct{ name, url, want string }{
		{"upper case http", "HTTP://example.com", `<a href="HTTP://example.com">x</a>`},
		{"mixed case https", "Https://example.com", `<a href="Https://example.com">x</a>`},
		{"upper case mailto", "MAILTO:me@example.com", `<a href="MAILTO:me@example.com">x</a>`},
		{"long s in https", "https\u017f://example.com", "x"},
		{"long s in http", "http\u017f://example.com", "x"},
		{"kelvin sign after a scheme", "\u212a://example.com", "x"},
		{"full-width scheme", "\uff48ttp://example.com", "x"},
	} {
		if got := ansiToHTML(osc8Link(tc.url, "x", st)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
