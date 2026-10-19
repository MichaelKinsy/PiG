//go:build !race

package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// Pairing the inline `$` of a paragraph reads from each opener to its closing delimiter. A paragraph of n openers that are
// rejected or taken, each with its own closing `$` a few characters on, costs n times that distance: the tokenizer may not
// copy or scan the rest of the paragraph for each opener (Pi's indexOf over a slice that shares the source's storage does
// not). The bytes a render allocates grow linearly with the paragraph.
func TestScalingInlineDollarPairing(t *testing.T) {
	for _, tc := range []struct{ name, unit string }{
		{"prices rejected by the digit and space rules", "costs $5 and $6 each, "},
		{"spans taken", "let $x^2$ and $a+b$ then "},
		{"escaped closers", `pay \$5 or $a\$b$ later, `},
		{"multibyte text between openers", "é $é+ü$ ü $5 ö "},
	} {
		testscaling.Check(t, testscaling.Case{Name: "markdown paragraph of n units, " + tc.name, Size: 250, Bound: testscaling.Linear, Prepare: func(n int) func() {
			text := strings.Repeat(tc.unit, n)
			return func() { NewMarkdown(text).Render(120) }
		}})
	}
}
