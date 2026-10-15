package tui

import "testing"

// A nested list is lexed from the text of the item that holds it, where a line the holding item took lazily stays as written: its indent counts against the nested item's content indent relative to the holding item, and the line it leaves for the next lazy decision is cut at that relative indent.
// Pi source: marked 18 Tokenizer.list (the item loop: `itemContents += '\n' + nextLine`, `prevLine = nextLine.slice(indent)`), the nested list lexed from the item's text; packages/tui/src/components/markdown.ts renderList.
func TestMarkdownNestedListLazyLineRelativeIndentMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"1. a\n   - b\n  x", "1. a\n   - b\n  ---|---\n  y", "- a\n  - b\n x", "- a\n  - b\n  x\n  y\n z", "- a\n  - b\n    - c\n  x", "- a\n  - b\n    - c\n   x", "- a\n  - b\n    - c\n    x\n y",
		"1. a\n   - b\n\n  x", "1. a\n   - b\n      x", "1. a\n   - b\n  ```\n  x", "1. a\n   - b\n  # h", "1. a\n   - b\n  > q", "1. a\n   - b\n  - c", "1. a\n   - b\n  ***\n  y", "1. a\n   - b\n  1. c",
		"  * b\n    * c\n--- | ---\nrow | row", "  * b\n    * c\n--- | ---\nrow", " * b\n   * c\n--- | ---\nrow | row", "  - b\n    - c\n--- | ---\nrow | row", "- a\n  - b\n    - c\n--- | ---\nrow", "- a\n  - b\n    - c\n  --- | ---\nrow",
		"  * b\n    * c\n      * d\n--- | ---\nrow | row", "* b\n  * c\n    * d\n--- | ---\nrow", "- a\n  - b\n    - c\n--- | ---\n--- | ---\nrow", "  * b\n    * c\n      * d\n    --- | ---\nrow", "  * b\n    * c\n      * d\n  --- | ---\nrow | row",
	})
}
