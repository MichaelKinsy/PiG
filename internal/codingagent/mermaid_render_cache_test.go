package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// One mermaid block, small enough to fit any width used here.
const mermaidFixture = "```mermaid\nflowchart LR\n  A[\"one\"] --> B[\"two\"]\n```\n"

func hasDiagram(lines []string) bool {
	return strings.Contains(strings.Join(lines, "\n"), "┌")
}

// blockUnderTest wires an assistant block the way newAssistantMessageBlock does:
// a transform that reads live streaming state and mode, plus the state
// fingerprint that keeps the render cache honest about them.
func blockUnderTest(streaming, mode *string) *tui.AssistantMessageBlock {
	block := tui.NewAssistantMessageBlock(false)
	block.SetMarkdownTransformState(func() string { return *mode + " " + *streaming })
	block.SetMarkdownTransform(func(markdown string, width int) string {
		ts := []extension.MarkdownTransformer{
			createMermaidMarkdownTransformer(func() string { return *mode }, nil),
		}
		isStreaming := *streaming == "streaming"
		return createMarkdownTransform(extension.MarkdownMessageAssistant, isStreaming, ts)(markdown, width)
	})
	block.SetTextDelta(mermaidFixture)
	return block
}

// In "final" mode the transform deliberately skips a streaming block, so the
// only render that draws the diagram is the one after the turn ends. The turn
// ending changes neither the text nor the width, so a cache keyed on those two
// alone serves the skipped render forever and the diagram never appears.
func TestFinalModeDrawsTheDiagramOnceTheTurnEnds(t *testing.T) {
	const width = 100
	streaming, mode := "streaming", "final"
	block := blockUnderTest(&streaming, &mode)

	if hasDiagram(block.Render(width)) {
		t.Fatal("final mode drew the diagram while the turn was still streaming")
	}
	streaming = "settled"
	if !hasDiagram(block.Render(width)) {
		t.Error("the turn ended and the diagram was still not drawn")
	}
}

// Turning mermaid rendering off mid-session must take a drawn diagram away, and
// turning it back on must bring it back, at unchanged text and width.
func TestChangingTheModeReRendersAnExistingBlock(t *testing.T) {
	const width = 100
	streaming, mode := "settled", "streaming"
	block := blockUnderTest(&streaming, &mode)

	if !hasDiagram(block.Render(width)) {
		t.Fatal("the diagram was not drawn under the default mode")
	}
	mode = "off"
	if hasDiagram(block.Render(width)) {
		t.Error("mermaid rendering was turned off and the diagram stayed on screen")
	}
	mode = "streaming"
	if !hasDiagram(block.Render(width)) {
		t.Error("mermaid rendering was turned back on and the diagram did not return")
	}
}

// The cache must still do its job. Counting transform runs rather than
// comparing output, because identical output proves only that the transform is
// deterministic, and stays green with the cache switched off.
func TestTheRenderCacheStillHoldsWhenNothingChanged(t *testing.T) {
	const width = 100
	runs := 0
	block := tui.NewAssistantMessageBlock(false)
	block.SetMarkdownTransformState(func() string { return "settled" })
	block.SetMarkdownTransform(func(markdown string, _ int) string {
		runs++
		return markdown
	})
	block.SetTextDelta(mermaidFixture)

	block.Render(width)
	block.Render(width)
	block.Render(width)
	if runs != 1 {
		t.Errorf("transform ran %d times for three renders at one state and width, want 1", runs)
	}

	block.Render(width + 1)
	if runs != 2 {
		t.Errorf("transform ran %d times after the width changed, want 2", runs)
	}
}
