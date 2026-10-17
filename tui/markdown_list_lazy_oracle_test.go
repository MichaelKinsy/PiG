package tui

import "testing"

// A line indented less than a list item's content continues the item's paragraph unless one of Tokenizer.list's six rules matches it: a bullet, a rule, a fence, a '#', an HTML tag start or a '>', each behind at most min(3, contentIndent-1) spaces.
func TestMarkdownListLazyLinesMatchPi(t *testing.T) {
	var docs []string
	for _, l := range []string{"_ _ _", "-----", "- - -", "***", "* * *", "#x", "# x", "#\u00a0x", "####### x", "~~~", "```x", "``` `", "<div>", "<span>", "<a href>", "<A HREF>", "<\u017fpan>", "<\u212ax>", "<1>", "<!-- c", "<!x", "</div>", "<b", "1. a", "10) a", "1234567890. a", "1.a", "+ a", "* a", "-", "-x", "> q", "| a |", "|---|", "    code", "  - nested", "   # h", "=", "[x]: /u", "x\ty", " # h", "  ```", "   > q", "    > q", "-\ta", "\u00a0- a"} {
		docs = append(docs, "- a\n"+l, "1. a\n"+l, "- a\n  b\n"+l, "10. a\n"+l, "-   a\n"+l, "1.  a\n"+l, "  - a\n"+l)
	}
	compareMarkdownWithPi(t, docs)
}
