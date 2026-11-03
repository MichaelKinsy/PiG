package tui

import "testing"

// markdownBlockEdgeDocs are the minimal documents on which PiG rendered other rows than pi-tui after the seeded random corpus and the sweeps compared them:
// each is a shape of marked's block lexer that the line-based block loop did not reproduce.
// Pi source: packages/tui/src/components/markdown.ts
func markdownBlockEdgeDocs() []string {
	return []string{
		// A quote whose list takes lazy lines, followed by a quoted line: marked lexes the quoted line apart from the list.
		"> - a\nx\n>b", "> - a\nx\n> b", "> - a\nx\n>  b", "> - **a\nb**\n>   **a\nb**",
		// A nested quote that is empty, and an empty quote line before an indented quoted line.
		">>\nb", "> >\nb", ">\n    >a",
		// A paragraph followed by a line of up to three spaces and display math: the display-math start clips the paragraph before the line's first character.
		"a\n  $$b", "b\n $$", "]\n $$", "🙂\n $$", "\\\n\t$$",
		// An item holding a nested list that spaces follow, then the next item.
		"- a\n  - b\n   \n\n- c", "- a\n  - b\n \n\n- c", "- *  \n-", "* *  \n*", "- * \n\n-",
		// Items with empty markers or an empty first line, followed by indented content.
		"-\n  * \n  1.", "* )\n      *\n\n*", "0. 1. `\n    <b>", "- a\n      b\n\n-",
		// Display math right after a list item, then a blank line and indented code.
		"- a\n$$x$$\n\n    code", "- a\n$$x$$\n\n  code", "- a\n\\[x\\]\n\n    code",
		// A lazy line of a quote that opens a table's delimiter row or an indented block.
		">$$\n?>", ">*\n\t a", ">~\n\t \\[a", ">- \\\n|\n=", "><div\n~~s~~", ">- http://b\nb\n-|-", ";\n\\[x\n-|-", "b\n\\[a\n-|-", "- *a\nb*\n-|-",
		// An unclosed fence whose last code line is a partial closing fence and whose text ends in a newline keeps that line.
		"```\n`\n", "~~~\n~~\n", "```\nx\n``\n",
	}
}

// TestMarkdownBlockEdgeDocsMatchPi renders markdownBlockEdgeDocs at narrow, middle and wide widths with and without padding, a default text color and
// preserved list markers, and requires the rows to equal pi-tui's.
func TestMarkdownBlockEdgeDocsMatchPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range markdownBlockEdgeDocs() {
		for _, apply := range []func(*markdownContractProbe){
			func(*markdownContractProbe) {},
			func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Color: true} },
			func(p *markdownContractProbe) { p.Options.PreserveOrderedListMarkers = true },
		} {
			for _, pad := range [][2]int{{0, 0}, {1, 1}} {
				probe := markdownContractProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Theme: "tagged", Ops: markdownContractRenderOps(2, 4, 11, 24, 80)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
