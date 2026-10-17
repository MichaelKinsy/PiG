package tui

import "testing"

// marked's list tokenizer strips a task item's checkbox from the last queued inline source that still starts with one, not from the item's own text.
// A task item whose first token is a setext heading queues such a source but never strips one, so an earlier task item's strip takes the heading's marker and leaves its own text unstripped.
// Pi source: marked 18 Tokenizer.list (the `for (const item of list.items)` task loop over lexer.inlineQueue) and packages/tui/src/components/markdown.ts renderList (taskMarker).
func TestMarkdownTaskMarkerStripTakesTheLastQueuedSourceMatchesPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"- [x] a\n\n- [ ] b\nS\n===", "- [ ] b\nS\n===", "- [x] a\n- [ ] b\nS\n===", "- [x] a\n- [ ] b\nS\n===\n- [ ] c",
		"- [x] a\n- [ ] b\nS\n===\n- [ ] c\nT\n===", "- [x] a\n- [ ] c\n- [ ] b\nS\n===", "- [x] a\n- [ ] b", "1. [x] a\n2. [ ] b\nS\n===",
		"- a\n  - [x] n\n  - [ ] m\nS\n===", "- [x] a\n\n- [ ] b\nS\n---",
	})
}
