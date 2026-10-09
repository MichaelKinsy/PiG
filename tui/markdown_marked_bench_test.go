package tui

import (
	"fmt"
	"strings"
	"testing"
)

// assistantMarkdownBody is an assistant reply of about tokens tokens (four bytes each) with the inline Markdown models emit: underscore and star emphasis, strong text, code spans, links, snake_case identifiers and a list.
func assistantMarkdownBody(tokens int) string {
	var sb strings.Builder
	for i := 0; sb.Len() < tokens*4; i++ {
		fmt.Fprintf(&sb, "Step %d updates `config_loader_%d.go` so that _every_ call to __load__ checks the **cache**; see [docs](https://example.com/a_%d) and keep snake_case_name.\n\n", i, i, i)
		fmt.Fprintf(&sb, "- ***Note*** %d: *a **nested** span* and ~~old~~ text\n- plain item with my_var_name\n\n", i)
	}
	return sb.String()[:tokens*4]
}

// BenchmarkMarkdownAssistantStreaming renders every streamed prefix of an assistant reply, as the chat component does after each text delta.
func BenchmarkMarkdownAssistantStreaming(b *testing.B) {
	for _, tokens := range []int{100, 2000, 10000} {
		full := assistantMarkdownBody(tokens)
		frames := max(1, tokens/50)
		b.Run(fmt.Sprintf("tokens=%d", tokens), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for f := 1; f <= frames; f++ {
					NewMarkdown(full[:len(full)*f/frames]).Render(100)
				}
			}
		})
	}
}

// BenchmarkMarkdownAdversarialDelimiters renders inputs that maximize delimiter and bracket scanning: long delimiter runs, unclosed openers and deep brackets.
func BenchmarkMarkdownAdversarialDelimiters(b *testing.B) {
	for _, input := range []struct{ name, text string }{
		{"star-run", strings.Repeat("*", 4000) + "x" + strings.Repeat("*", 4000)},
		{"underscore-run", strings.Repeat("_", 4000) + "x" + strings.Repeat("_", 4000)},
		{"unclosed-strong", strings.Repeat("**a ", 2000)},
		{"unclosed-em-underscore", strings.Repeat("_a ", 2000)},
		{"alternating", strings.Repeat("*_", 2000) + "x" + strings.Repeat("_*", 2000)},
		{"nested-brackets", strings.Repeat("[", 2000) + "x" + strings.Repeat("]", 2000) + "(u)"},
		{"unclosed-brackets", strings.Repeat("[a](", 2000)},
		{"unclosed-tags", strings.Repeat("<a ", 2000)},
	} {
		b.Run(input.name, func(b *testing.B) {
			b.SetBytes(int64(len(input.text)))
			b.ReportAllocs()
			for b.Loop() {
				NewMarkdown(input.text).Render(100)
			}
		})
	}
}
