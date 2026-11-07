package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// buildLargeChat builds a Container that stands in for a long transcript:
// `msgs` markdown blocks, each `linesPer` lines of prose. This is the
// per-frame cost the interactive chat pays every keystroke and every
// streaming delta, since Container.Render re-walks every child and the
// caller re-applies line resets and diffs the whole buffer.
func buildLargeChat(msgs, linesPer int) (*Container, int) {
	c := NewContainer()
	total := 0
	for i := range msgs {
		var sb strings.Builder
		for j := range linesPer {
			fmt.Fprintf(&sb, "This is line %d of assistant message %d with some `code` and **bold**.\n", j, i)
		}
		c.Add(NewMarkdown(sb.String()))
		total += linesPer
	}
	return c, total
}

// BenchmarkContainerRender measures assembling the full transcript buffer
// once (markdown blocks are cached internally, so this is the steady-state
// cost of a frame where the transcript did not change, e.g. a keystroke).
func BenchmarkContainerRender(b *testing.B) {
	c, _ := buildLargeChat(2000, 4)
	const width = 100
	_ = c.Render(width) // warm the per-child markdown caches
	b.ResetTimer()
	for range b.N {
		_ = c.Render(width)
	}
}

// BenchmarkContainerRenderPlusResets adds the ApplyLineResets pass the
// doRender loop runs over every line every frame.
func BenchmarkContainerRenderPlusResets(b *testing.B) {
	c, _ := buildLargeChat(2000, 4)
	const width = 100
	_ = c.Render(width)
	b.ResetTimer()
	for range b.N {
		lines := c.Render(width)
		_ = widthx.ApplyLineResets(lines)
	}
}
