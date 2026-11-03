package tui

import "testing"

// A streamed code block ends before the partial closing fence of its last line (markdown.ts trimPartialClosingFences), also in the last item of a list and the last
// line of a quote; the fence is only partial when it is shorter than the opening one and made of the same character.
func TestMarkdownPartialClosingFencesMatchPi(t *testing.T) {
	compareMarkdownWithPi(t, []string{
		"```\n`", "```\n``", "```\ncode\n`", "```\ncode\n``", "```js\nx\n``", "```\ncode\n```", "```\ncode\n```\n", "````\ncode\n```", "````\ncode\n``", "````\ncode\n`````",
		"~~~\n~", "~~~\nabc\n~~", "~~~\nabc\n~~~", "~~~\nabc\n`", "```\ncode\n~~", "~~~~\nx\n~~~", "```\ncode\n`x", "```\ncode\n ``", "```\n\n`", "```\ncode\n\n``",
		"   ```\ncode\n``", "  ```\n  code\n  ``", " ~~~\nx\n~", "```\n``\n`", "```\n`\ncode", "```\nline one\nline two\n``",
		"- ```\n  x\n  `", "- a\n- ```\n  x\n  ``", "- ```\n  x\n  ``\n- b", "1. ```\n   x\n   `", "> ```\n> x\n> `", "> ```\n> x\n> ``\n\ntext", "> - ```\n>   x\n>   `", "- > ```\n  > x\n  > ``",
		"text\n\n```\ncode\n``", "```\ncode\n``\n\ntext", "# h\n\n```ts\nconst a = 1;\n``",
		"    indented\n    ``", "\t```\n``", "```\r\ncode\r\n``", "```\u2028\n``",
	})
}

// An ordered or unordered list keeps the marker of its source when the option asks for it: up to three spaces before it, one to nine digits and a dot or parenthesis
// for the ordered one, "-", "+" or "*" for the other, followed by a space, a tab, a line break or the end of the text.
func TestMarkdownPreservedListMarkersMatchPi(t *testing.T) {
	docs := []string{
		"1. a\n2. b", " 1) a\n 2) b", "  3. x\n  4. y", "   9. z", "10) y\n11) z", "123456789. nine\n123456790. next", "1234567890. ten", "1.\ttab\n2.\ttab", "7.\nnext", "0. zero\n1. one", "5. a\n6. b\n\n   nested\n7. c",
		"3. a\n3. b\n3. c", "9. a\n9. b", "19. a\n19. b", "5. a\n5. b", "1) a\n1) b", "7. x\n1. y", "123456789. a\n123456789. b", "+\r\nx\n+\r\ny", "*\r\nx", "+\t a\n+\t b", "1.x", "1.", "1. ", "12.  two spaces", "1)\ta", "1.\u00a0a",
		"- a\n- b", "+ a\n+ b", "* a\n* b", "   - d", "  + d\n  + e", "-\tt", "-\ta\n-\tb", "-", "- ", "-\n- a", "-\r\n- a", "+\nx", "*\n\n  text", "-a", "--", "* * *\n- a",
		"- a\n  - b\n    - c", "1. a\n   - b\n   - c\n2. d", "- [ ] t\n- [x] u", "1. [ ] t\n2. [x] u", "> - a\n> - b", "> 1. a\n> 2. b", "- 1. a\n  2. b",
	}
	var probes []markdownContractProbe
	for _, doc := range docs {
		for _, preserve := range []bool{true, false} {
			probes = append(probes, markdownContractProbe{Text: doc, Theme: "tagged", Options: markdownContractOptions{PreserveOrderedListMarkers: preserve}, Ops: markdownContractRenderOps(1, 2, 3, 4, 20, 40)})
		}
	}
	if failures := compareMarkdownContract(t, probes); failures > 0 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}
