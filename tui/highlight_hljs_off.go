//go:build pig_strip_syntax_highlight

package tui

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): a Piglet Binary built with pig_strip_syntax_highlight does not link highlight.js. Code renders as Pi renders a language highlight.js does not support.

func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.SyntaxHighlight) }

// highlightEngine is highlight.js with no language registered.
type highlightEngine = plainHighlightEngine

type plainHighlightEngine struct{}

func highlightRegistry() highlightEngine { return plainHighlightEngine{} }

func highlightStripped() bool { return true }

func (plainHighlightEngine) LoadAllLanguages() {}

func (plainHighlightEngine) SupportsLanguage(string) bool { return false }

func (plainHighlightEngine) Highlight(_, language string, _ bool) (string, error) {
	return "", unknownHighlightLanguage(language)
}

func (plainHighlightEngine) HighlightAuto(code string, _ []string) (string, error) {
	return plainHighlightHTML(code), nil
}
