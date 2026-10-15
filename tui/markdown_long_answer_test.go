package tui

import (
	"strconv"
	"strings"
	"testing"
)

// longAnswerSection is one section of the long Markdown answer the long-answer benchmark and gates stream: the stream of the
// bolt-bench "long answer" scenario (headings, a nested list, a code block, a table and a quote).
const longAnswerSection = "## Section {n}: what the function does\n\nThe `parse()` function reads **one token at a time** and keeps a *small* stack; see [the grammar](https://example.com/g/{n}).\nIt handles these cases:\n\n- plain text, with `inline code` and a long line that has to be wrapped by the terminal because it does not fit in its width at all\n- nested lists:\n  1. first, with a number {n}\n  2. second, with ~~strikethrough~~ and unicode: naïve café 日本語 😀\n\n```ts\nexport function parse{n}(input: string): Node[] {\n\tconst out: Node[] = [];\n\tfor (let i = 0; i < input.length; i++) {\n\t\tif (input.charCodeAt(i) === 0x5b) out.push({ kind: \"open\", at: i });\n\t}\n\treturn out;\n}\n```\n\n| column | value | note |\n|---|---|---|\n| a{n} | 1 | first |\n| b{n} | 22 | second, longer |\n\n> A quote that closes the section, number {n}.\n\n"

// longAnswer is size bytes of consecutive sections.
func longAnswer(size int) string {
	var body strings.Builder
	for n := 1; body.Len() < size; n++ {
		body.WriteString(strings.ReplaceAll(longAnswerSection, "{n}", strconv.Itoa(n)))
	}
	return body.String()[:size]
}

// BenchmarkLongAnswerStream streams the answer 24 bytes at a time into a Markdown, as a fast model does.
func BenchmarkLongAnswerStream(b *testing.B) {
	for _, size := range []int{5000, 20000, 60000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			body := longAnswer(size)
			for range b.N {
				md := NewMarkdown("")
				for end := 24; ; end += 24 {
					end = min(end, len(body))
					md.SetText(body[:end])
					md.Render(160)
					if end == len(body) {
						break
					}
				}
			}
		})
	}
}
