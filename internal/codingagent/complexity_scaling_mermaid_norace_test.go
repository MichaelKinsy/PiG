//go:build !race && !pig_strip_mermaid

package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

func TestScalingMermaidTransformOfAStreamedMessage(t *testing.T) {
	// The Mermaid transform runs on the whole message every frame; a message without a Mermaid fence costs no parse.
	testscaling.Check(t, testscaling.Case{Name: "mermaid transform of a message of n paragraphs", Size: 50, Bound: testscaling.Constant, Prepare: func(n int) func() {
		var text strings.Builder
		for i := range n {
			fmt.Fprintf(&text, "Paragraph %d with `code` and a list:\n\n- one\n- two\n\n```go\nfunc f() {}\n```\n\n", i)
		}
		message := text.String()
		return func() {
			transformMermaidBlocks(message, extension.MarkdownTransformContext{AvailableWidth: 80, IsStreaming: true}, nil)
		}
	}})
}
