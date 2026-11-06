package tui

import "testing"

// markdownContractRegressionDocs are the documents on which PiG rendered other rows than pi-tui before the contract tests compared the constructor variants: a
// paragraph that ends in a line of spaces under a default text color, preserved ordered markers of items with no content after the delimiter, a narrow quote
// whose list takes lazy lines, a bare "-" under an item's text (a setext underline), and an item with an empty first line that a blank line ends.
func markdownContractRegressionDocs() []string {
	return []string{
		"a\n   ", "a\n  \n", "a\n   \nb",
		"2) \nnext", "2)\nnext", "2) \n3) b", "2)\n3) b", "1) a\n2)", "1) a\n2) ", "3.  \n4. b", "- \n- b",
		"> - a\nb\n\nx", "> - a \nb\n\nx", "> - a\t\nb\n\nx", "> - a  \nb\n\nx", "> - a\n> b\nc\n\nx", "> - a\nb\n\n", "> - a\nb\n\n\nx", "> - a\n>\nb\n\nx",
		"> - a\n> b\n\nx", "> a\nb\n\nx", "> > - a\nb\n\nx", "> - a\nb\n\n# h", "> - a\nb\n\n- c", "> 1. a\nb\n\nx", "> - a\n>   - b\nc\n\nx", "> - a\n> - b \nc\n\nx", "> - a\n> - b  \nc",
		"2. two\n   -", "- two\n  -", "- two\n  - ", "- a\n  two\n  -", "- two\n -", "- two\n\n  -", "1. one\n2. two\n   -", "- two\n  *", "- \n  -",
		"- \n\n  -", "- \n\n  - a", "- \n\n  a", "1. \n\n   1. a", "- \n\n- a", "- \n\n\n  a", "- \n  \n  a", "- x\n\n  a", "- \n\n  a\n- b", "- \n\n  ```\n  c\n  ```",
	}
}

// TestMarkdownRegressionDocsMatchPi renders markdownContractRegressionDocs at widths around the narrow-quote limit with the default constructor, a default text
// color and preserved list markers, and requires the rows to equal pi-tui's.
// Pi source: packages/tui/src/components/markdown.ts
func TestMarkdownRegressionDocsMatchPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range markdownContractRegressionDocs() {
		for _, apply := range []func(*markdownContractProbe){
			func(*markdownContractProbe) {},
			func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Color: true} },
			func(p *markdownContractProbe) { p.Options.PreserveOrderedListMarkers = true },
		} {
			for _, pad := range [][2]int{{0, 0}, {1, 1}} {
				probe := markdownContractProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Theme: "tagged", Ops: markdownContractRenderOps(1, 2, 3, 4, 24)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
