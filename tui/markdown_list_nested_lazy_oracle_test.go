package tui

import "testing"

// A line that continues a nested list item's paragraph reaches the nested list's tokenizer after the enclosing item cut its own content indent off it (marked Tokenizer.list: nextLine.slice(indent)), so `- a`, `  - b`, `  x` renders x at b's text column, not two columns further. An item whose first line is empty and whose next line starts a nested list has the text "\n- b": the leading newline is a space token that renders the bullet with an empty row.
// Pi source: marked 18 Tokenizer.list; packages/tui/src/components/markdown.ts renderList.
func TestMarkdownNestedListLazyLineAndEmptyItemMatchPi(t *testing.T) {
	docs := []string{
		"- a\n  - b\n  x", "- a\n  - b\n  x\n  y", "- a\n  - b\n   x", "- a\n  - b\n x", "- a\n  - b\nx", "- a\n  - b\n    x",
		"- a\n  - b\n    - c\n  y", "- a\n  - b\n    - c\n    y", "- a\n  - b\n    - c\n      z", "* a\n  * b\n  x", "1. a\n   1. b\n   x", "1. a\n   - b\n   x",
		"-\n  - b", "-\n    - c", "- \n  - b", "1.\n   - c", "- a\n-\n  - b", "-\n  - b\n  - c", "-\n  foo", "-\n  foo\n  - b", "- -\n  - x",
	}
	compareMarkdownWithPi(t, docs)
}

// A rule written with the list's own marker (`* * *`, `- - -`) after a blank line ends the list: Tokenizer.list stops at the hr rule before it reads the blank line as part of a sibling, so a space token follows the list.
// marked decides a lazy line from the outermost item down with that item's last line cut by its content indent, so a line that an outer item refuses (its last line is an indented code line) is no lazy line of a deeper nested list, and a line indented to an item's content is no lazy line of it.
// Pi source: marked 18 Tokenizer.list; packages/tui/src/components/markdown.ts renderList.
func TestMarkdownListRuleWithListMarkerAndDeepNestedLazyLinesMatchPi(t *testing.T) {
	docs := []string{
		"* star\n\n* * *", "- a\n\n- - -", "- a\n\n- - -\n- b", "* a\n\n* * *\n\nx", "- minus\n* star\n\n* * *\n", "- a\n\n***", "- a\n\n---", "* a\n* * *", "- a\n\n- b", "1. a\n\n1. b",
		"- a\n    - b\n        - c\nx", "- a\n    - b\n        - c\n\nx", "- a\n  - b\n    - c\nx", "- a\n    - b\nx", "- a\n  - b\n      - c\nx", "1. a\n   - b\n      - c\nx",
		"- a\n    - b\n        - c\n    text", "- a\n    - b\n        - c\n  text", "- a\n    - b\n        - c\n      text", "- a\n    - b\n        - c\n        text", "- a\n    - b\n        - c\n    code", "- a\n    - b\n        - c\n    - d",
		"- a\n    - b\n        - c\nText\n```", "- a\n    - b\n        - c\nSetext\n===", "- a\n    - b\n        - c\n| h1 | h2 |",
	}
	compareMarkdownWithPi(t, docs)
}

// The items of a top-level list each have their own marker indent (Tokenizer.list reads `^( {0,3}bull)` for every item), so `  - b`, a blank line, `    p` and `- two` are one loose list and its items are separated by an empty row.
// Pi source: marked 18 Tokenizer.list; packages/tui/src/components/markdown.ts renderList (`token.loose && !isLastItem`).
func TestMarkdownListItemsWithDifferentMarkerIndentsAreOneListMatchPi(t *testing.T) {
	docs := []string{
		"  - b\n\n    p\n- two", " - b\n\n   p\n- two", "  - b\n\n    p\n - two", "  - b\n\n    p\n  - two", "- b\n\n  p\n- two", "- a\n - b\n  - c\n   - d", "   - a\n  - b\n - c\n- d", "  - b\n- two",
		"- a\n\n - b\n\n  - c", "1. a\n\n  2. b\n\n 3. c", "- a\n   - b\n - c", "  - a\n      - b\n- c", "    - four spaces is code\n- x", "- a\n\n    - b\n\n  - c",
	}
	compareMarkdownWithPi(t, docs)
}
