//go:build !pig_strip_syntax_highlight

package tui

import (
	"sync"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
	"github.com/MichaelKinsy/PiG/tui/internal/hljs"
)

// pig additive (D92): this file links highlight.js; a Piglet Binary built with pig_strip_syntax_highlight uses highlight_hljs_off.go instead.

// highlightEngine is the highlight.js instance type.
type highlightEngine = *hljs.Registry

// highlightRegistry is the highlight.js instance with the languages syntax-highlight.ts registers when it loads.
var highlightRegistry = sync.OnceValue(hljs.NewRegistry)

// highlightStripped reports whether the active Piglet strips syntax-highlight. Stock PiG never does.
func highlightStripped() bool {
	return pigstrip.Has(pigstrip.ListFeatures, pigstrip.SyntaxHighlight)
}
