//go:build !pig_strip_mermaid

package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// A streaming transformer remembers the last text it found plain (mermaidPlainText) and checks only what follows it. Every
// frame must equal what transformMermaidBlocks, which keeps no memory, returns: a "\r" that arrives after plain frames (the
// text is then normalized), "mermaid" split across frames ("mer" then "maid"), a text that does not extend the remembered
// one, and the render after streaming ends.
func TestMermaidStreamedTransformerEqualsTheWholeTransform(t *testing.T) {
	mode := func() string { return "streaming" }
	streaming := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, IsStreaming: true, AvailableWidth: 80}
	shared := createMermaidMarkdownTransformer(mode, nil)
	check := func(text string, context extension.MarkdownTransformContext) {
		t.Helper()
		got := shared(text, context)
		if want := transformMermaidBlocks(text, context, nil); got != want {
			t.Fatalf("streaming=%v frame %q:\ngot  %q\nwant %q", context.IsStreaming, text, got, want)
		}
	}
	withCR := "A plain paragraph of the answer.\n\nA second line ends in a carriage return\r\nand the text goes on.\n"
	withWord := "Here is the flow, a diagram:\n\n```mer" + "maid\ngraph TD\n  A --> B\n```\n\nAfter the diagram.\n"
	if strings.HasPrefix(withWord, withCR) {
		t.Fatal("the second text must not extend the first")
	}
	for _, text := range []string{withCR, withWord} {
		for end := 1; end <= len(text); end++ {
			check(text[:end], streaming)
		}
	}
	if final := createMermaidMarkdownTransformer(mode, nil)(withWord, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 80}); final == withWord {
		t.Fatal("the finished text was not transformed: the case does not exercise a diagram")
	}
	streaming.IsStreaming = false
	check(withWord, streaming)
}
