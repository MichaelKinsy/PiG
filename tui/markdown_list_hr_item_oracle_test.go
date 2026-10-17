package tui

import "testing"

// A list item line that the hr rule matches ('- - -', '* * *') starts no list: marked's Tokenizer.list stops at it, so inside an item it is content that lexes as a
// rule. parseMarkdownList returned without consuming such a line, and the item loop treated it as a nested list again forever (`- a\n\n  - - -` and `- * * *` hung
// Render and grew memory without bound).
// Pi source: marked 18 Tokenizer.list (the hr check before each item) and packages/tui/src/components/markdown.ts renderList.
func TestMarkdownListItemRuleLineIsContentNotANestedListMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"- a\n\n  - - -", "- a\n  - - -", "- a\n\n  * * *", "- a\n\n   - - -\n  b", "1. a\n\n   - - -", "- a\n\n  - - -\n\n  c",
		"- * * *", "- - - - x", "* - - -", "- a\n\n  ***", "- a\n\n  ---",
		// The rule is judged on the line after the item's content indent: item b's content "- - -" is a rule, whatever depth the item sits at.
		"- a\n  - b\n\n    - - -", "- a\n  - b\n    - - -", "- a\n  - b\n\n     - - -", "1. a\n   - b\n\n     - - -", "- a\n\n      - - -",
	})
}
