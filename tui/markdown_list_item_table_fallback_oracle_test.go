package tui

import "testing"

// A list item renders each of its tokens with no next token (renderList passes undefined to renderToken), so a table too narrow for its columns adds no empty row of its own after the raw text it falls back to.
// Pi source: packages/tui/src/components/markdown.ts renderList (`this.renderToken(itemToken, itemWidth, undefined, ...)`) and renderTable (`if (nextTokenType && nextTokenType !== "space") fallbackLines.push("")`).
func TestMarkdownListItemTableFallbackAddsNoSpacingRowMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"- two\n| a |\n|---|\n   ```", "- | a | b |\n  |---|---|\n  ```\n  x\n  ```", "1. | a | b |\n   |---|---|\n   | 1 | 2 |\n   para", "- | a |\n  |---|\n  > q",
		"- a\n  | a | b | c |\n  |---|---|---|\n  | 1 | 2 | 3 |\n  # H", "> | a |\n> |---|\n> ```\n> x\n> ```", "| a |\n|---|\n```\nx\n```",
	})
}
