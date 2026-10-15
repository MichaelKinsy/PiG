//go:build !race && linux

package hljs

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// Loading highlight.js is one-time work on every interactive start: the eager grammars on the first highlight, the text tables on the first case-insensitive or non-ASCII comparison, the rest on a background task. It reads the embedded snapshots without building a generic JSON tree.
func TestGrammarLoadAllocationGate(t *testing.T) {
	work := perfgate.Measure(func() {
		NewRegistry().LoadAllLanguages()
		decodeSnapshot(eagerGrammars, snapshotTables)
	})
	perfgate.Check(t, "tui.hljs_grammar_load.allocs", work.Allocs)
	perfgate.Check(t, "tui.hljs_grammar_load.bytes", work.Bytes)
}
