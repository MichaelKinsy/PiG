package tui

import (
	"strings"
	"testing"
)

// BenchmarkLongStreamingAssistantMessage streams a long answer (about 60 KB: prose, lists and a fenced code block every
// few paragraphs) in 150-byte deltas, drawing a frame after each, the path of an interactive message_update. "default"
// draws with the active theme's own highlighter; "foreign" draws with a Markdown theme whose highlighter tui does not
// know (here highlight.js without the memo), which no resume point or memo serves, so every code block of every frame
// depends on the reuse of the Markdown.
// One op is the whole stream.
func BenchmarkLongStreamingAssistantMessage(b *testing.B) {
	full := streamingBody(60000)
	foreign := func() *MarkdownTheme {
		theme := GetMarkdownTheme()
		theme.HighlightCode = func(code, lang string) []string {
			return highlightCodeUncached(highlightRegistry(), code, lang, ActiveTheme())
		}
		return &theme
	}
	for _, c := range []struct {
		name  string
		theme func() *MarkdownTheme
	}{{"default", func() *MarkdownTheme { return nil }}, {"foreign", foreign}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				block := NewAssistantMessageComponent(nil, false, c.theme(), "", nil, nil, "", "")
				for end := 0; end < len(full); end += 150 {
					block.SetTextDelta(strings.Clone(full[end:min(end+150, len(full))]))
					block.Render(100)
				}
				block.FinishStreaming()
				block.Render(100)
			}
		})
	}
}
