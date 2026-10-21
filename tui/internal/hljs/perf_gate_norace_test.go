//go:build !race && linux

package hljs

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
)

// Loading highlight.js on every interactive start: the eager grammars on the first highlight and the text tables on the first case-insensitive or non-ASCII comparison. Registering the rest of the languages (LoadAllLanguages) decodes none of them; it reads the embedded snapshots without building a generic JSON tree.
func TestGrammarLoadAllocationGate(t *testing.T) {
	work := perfgate.Measure(func() {
		NewRegistry().LoadAllLanguages()
		decodeSnapshot(eagerGrammars, snapshotTables)
	})
	perfgate.Check(t, "tui.hljs_grammar_load.allocs", work.Allocs)
	perfgate.Check(t, "tui.hljs_grammar_load.bytes", work.Bytes)
}

// A session that shows code in a language outside the eager set pays once: inflating and indexing the full snapshot, then decoding that language and what it reaches.
func TestGrammarFirstUseAllocationGate(t *testing.T) {
	r := loadedRegistry()
	work := perfgate.Measure(func() {
		r = loadedRegistry()
		if !r.SupportsLanguage("yaml") {
			t.Error("yaml is not supported")
		}
	})
	perfgate.Check(t, "tui.hljs_language_first_use.allocs", work.Allocs)
	perfgate.Check(t, "tui.hljs_language_first_use.bytes", work.Bytes)
}

// The first auto-detection over every language decodes the whole full snapshot, as every start did before languages loaded on first use.
func TestGrammarAutoDetectLoadAllocationGate(t *testing.T) {
	work := perfgate.Measure(func() {
		r := loadedRegistry()
		if _, err := r.HighlightAuto("x", []string{"yaml"}); err != nil {
			t.Error(err)
		}
		r.decodeAllDeferred()
	})
	perfgate.Check(t, "tui.hljs_autodetect_load.allocs", work.Allocs)
	perfgate.Check(t, "tui.hljs_autodetect_load.bytes", work.Bytes)
}
