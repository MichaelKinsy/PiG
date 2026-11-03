package tui

import "testing"

// markdownKnownDifferenceProbes are minimal documents on which PiG still renders other rows than pi-tui: a shape of marked's block lexer that a class of the random corpus reached and that the port does not reproduce. Each stays here, with the reason, until it is fixed. None is left.
var markdownKnownDifferenceProbes = []string{}

// TestMarkdownKnownDifferencesStillDiffer requires every probe of markdownKnownDifferenceProbes to differ from pi-tui at one of the widths of the random corpus, padded or not, so that fixing a difference makes
// the test fail until the probe leaves the list.
func TestMarkdownKnownDifferencesStillDiffer(t *testing.T) {
	if len(markdownKnownDifferenceProbes) == 0 {
		return
	}
	var probes []markdownContractProbe
	for _, doc := range markdownKnownDifferenceProbes {
		for padding := range 2 {
			probes = append(probes, markdownContractProbe{Text: doc, PaddingX: padding, Theme: "tagged", Ops: markdownContractRenderOps(5, 11, 24, 80)})
		}
	}
	differences, _ := markdownContractDifferences(t, probes)
	for i, doc := range markdownKnownDifferenceProbes {
		_, plain := differences[2*i]
		_, padded := differences[2*i+1]
		if !plain && !padded {
			t.Errorf("%q matches Pi now: remove it from markdownKnownDifferenceProbes", doc)
		}
	}
}
