package tui

import (
	"fmt"
	"strings"
)

// pig additive (D92): highlight.js with no language registered, for a Piglet that strips syntax-highlight.

// highlightPlain is highlightWith on a highlight.js instance with no language registered: hljs.highlight throws for every language, and hljs.highlightAuto returns its plaintext result, the escaped code.
func highlightPlain(code string, options HighlightOptions) (string, error) {
	if options.Language != "" {
		return "", unknownHighlightLanguage(options.Language)
	}
	return renderHighlightedHTML(plainHighlightHTML(code), options.Theme)
}

// unknownHighlightLanguage is the error hljs.highlight throws for a language it does not have.
func unknownHighlightLanguage(language string) error {
	return fmt.Errorf(`Unknown language: "%s"`, language)
}

var plainHighlightEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

// plainHighlightHTML is highlight.js escapeHTML, the plaintext result of hljs.highlightAuto.
func plainHighlightHTML(code string) string { return plainHighlightEscaper.Replace(code) }
