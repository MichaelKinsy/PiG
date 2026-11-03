package tui

import "testing"

// markdownResidualDocs are the minimal documents on which PiG rendered other rows than pi-tui when the block lexer was a loop over lines that imitated marked's quote, list and paragraph rules: nested quotes with lazy lines,
// lists inside quotes, empty and indented list items, a table, display math or an indented block after a lazy line, and a carriage return inside a quote. They render the same now that the block lexer is a port of marked's Lexer.blockTokens
// and Tokenizer rules, which Lexer.ts runs on the text itself.
// Pi source: node_modules/marked/src/Lexer.ts, node_modules/marked/src/Tokenizer.ts, packages/tui/src/components/markdown.ts
func markdownResidualDocs() []string {
	return []string{
		"-\t *\n  d",
		">>a\n>\t\n|",
		">>*\n>b\n$",
		">>>\n>\t\n]",
		">>]\n>\t\n$",
		">*\nb\n\t>*",
		">-\n>\tb \n]",
		"a\n* \n-\n\\[",
		"- a\n$$\n\t*",
		"~\n\\[\na\n-|-",
		">* *  \n  =",
		">* >  \n  `",
		">b\nb\nz\n-|-",
		">* *\n>_ \ns",
		">1. *  \n\te",
		">b\nt\ns||\n-|",
		">`\n    `\n>\t",
		"-     a\n  -",
		">-\n>   z \n$",
		">-\n>z\n    ;",
		">a\n    b\n>\t",
		">*\t>\t\n    a",
		"z\n\\[\nd||\n-|",
		"+\n  >\t\n   \n+",
		">z\n    b\n>\n>",
		">a\n    b\n>  ",
		"b\n    |\n-\n$$",
		"b\n    _\n-\n\\[",
		">\\[\n>- \\\n$\n=",
		">>a\n>  \n🙂",
		"**\n    |\n-\n\\[",
		"**\n    `\n-\n$$",
		"-\n  >  \n   \n-",
		"><v>\n>)\n    >",
		"*\n  d||\n-|-\n  *",
		"- [ ] z\n      =",
		">a\n    ```\n>b\na",
		"+ `\n\n      `\n  *",
		">- b\n$\n>     *\n]",
		">   - )\n***x***\n=",
		"> a$$b\n*c*",
		"> a$$**\n\\*",
		"> .$$(****\n\\*",
		"> \\[\ré",
		"> \\[l](u)-\r(\x1b[22mé",
		"> \\[\r",
		"> \\[\ra",
	}
}

// TestMarkdownResidualDocsMatchPi renders markdownResidualDocs under the stock themes, a default text style and padding at widths from one column to eighty, and requires the rows to equal pi-tui's.
func TestMarkdownResidualDocsMatchPi(t *testing.T) {
	var probes []markdownContractProbe
	for _, doc := range markdownResidualDocs() {
		for _, apply := range []func(*markdownContractProbe){
			func(*markdownContractProbe) {},
			func(p *markdownContractProbe) { p.Style = &markdownContractStyle{Color: true} },
			func(p *markdownContractProbe) {
				p.Theme = "dark"
				p.Style = &markdownContractStyle{Bold: true, Italic: true}
			},
			func(p *markdownContractProbe) {
				p.Theme, p.Links = "light", true
				p.Style = &markdownContractStyle{Bold: true, Strikethrough: true}
			},
		} {
			for _, pad := range [][2]int{{0, 0}, {1, 0}, {1, 1}} {
				probe := markdownContractProbe{Text: doc, PaddingX: pad[0], PaddingY: pad[1], Theme: "tagged", Ops: markdownContractRenderOps(1, 2, 3, 4, 5, 7, 11, 24, 40, 80)}
				apply(&probe)
				probes = append(probes, probe)
			}
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
