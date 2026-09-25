package tui

import (
	"fmt"
	"strings"
	"testing"
)

// streamingBody builds a realistic assistant message of roughly n characters,
// mixing prose, a bulleted list, and a fenced code block (the expensive path).
func streamingBody(n int) string {
	var sb strings.Builder
	i := 0
	for sb.Len() < n {
		fmt.Fprintf(&sb, "Here is explanatory prose paragraph %d about the change.\n\n", i)
		fmt.Fprintf(&sb, "- point %d about the design\n- another with `inline`\n", i)
		// Distinct code per block (embeds i) so a naive re-highlight cannot
		// dedupe within a frame; only a cross-frame memo of closed blocks helps.
		fmt.Fprintf(&sb, "```go\nfunc example%d(x int) int {\n\treturn x*%d + 1\n}\n```\n", i, i)
		i++
	}
	return sb.String()[:n]
}

// BenchmarkMarkdownStreaming models the streaming path WITHOUT the highlight
// memo: SetTextDelta sets md.Content to the full accumulated text and
// invalidates, so every frame re-parses and re-highlights the whole growing
// message. Clearing the highlight cache each frame reproduces the pre-memo
// O(M^2) cost over a turn.
func BenchmarkMarkdownStreaming(b *testing.B) {
	const finalLen = 8000
	const frames = 120
	full := streamingBody(finalLen)
	const width = 100
	b.ResetTimer()
	for range b.N {
		for f := 1; f <= frames; f++ {
			hlMu.Lock()
			clear(hlCache) // no memo: re-highlight everything every frame
			hlMu.Unlock()
			end := finalLen * f / frames
			md := NewMarkdown(full[:end])
			_ = md.Render(width) // cold parse of the whole prefix, every frame
		}
	}
}

// BenchmarkMarkdownParseFinal measures a single parse of the final message,
// the floor a perfect incremental renderer would approach.
func BenchmarkMarkdownParseFinal(b *testing.B) {
	full := streamingBody(8000)
	const width = 100
	b.ResetTimer()
	for range b.N {
		md := NewMarkdown(full)
		_ = md.Render(width)
	}
}
