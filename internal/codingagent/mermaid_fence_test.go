//go:build !pig_strip_mermaid

package codingagent

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/markdowntransform"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi 0.87.1's mermaid.ts delegates fence recognition to Marked. Probing its shipped transformer with each length below yields these exact bytes; the fence has no RE2 counted-repetition limit.
func TestMermaidLongFences(t *testing.T) {
	t.Parallel()
	const want = "Before\n\n`┌───┐    ┌───┐`  \n`│ A ├───▶│ B │`  \n`└───┘    └───┘`\n\n\nAfter"
	transform := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	for _, length := range []int{3, 4, 1000, 1001, 4096} {
		t.Run(strconv.Itoa(length), func(t *testing.T) {
			fence := strings.Repeat("`", length)
			markdown := "Before\n\n" + fence + "mermaid\nflowchart LR\n  A --> B\n" + fence + "\n\nAfter"
			got := transform(markdown, extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 80})
			if got != want {
				t.Fatalf("transform = %q, want %q", got, want)
			}
		})
	}
}

func TestMermaidLongFenceAssistantBlock(t *testing.T) {
	transform := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	render := func(length int) []string {
		fence := strings.Repeat("`", length)
		block := tui.NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		block.SetMarkdownTransform(markdowntransform.CreateMarkdownTransform(extension.MarkdownMessageAssistant, false, []extension.MarkdownTransformer{transform}))
		block.SetTextDelta(fence + "mermaid\nflowchart LR\n  A --> B\n" + fence)
		return block.Render(80)
	}
	want := strings.Join(render(3), "\n")
	if !strings.Contains(want, "│ A ├───▶│ B │") {
		t.Fatalf("ordinary fence did not render a diagram: %q", want)
	}
	if got := strings.Join(render(4096), "\n"); got != want {
		t.Fatalf("long-fence render = %q, want %q", got, want)
	}
}

func TestMermaidFenceClose(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		line   string
		length int
		want   bool
	}{
		{"empty", "", 3, false},
		{"short", "``", 3, false},
		{"equal", "```", 3, true},
		{"longer", "`````", 4, true},
		{"shorter", "```", 4, false},
		{"three spaces", "   ```", 3, true},
		{"four spaces", "    ```", 3, false},
		{"leading tab", "\t```", 3, false},
		{"trailing spaces", "```   ", 3, true},
		// Marked's closing fence ends in literal spaces, not JavaScript whitespace.
		{"trailing spaces and tabs", "``` \t ", 3, false},
		{"mixed suffix", "```~`~~", 3, true},
		{"trailing text", "```mermaid", 3, false},
		{"separated ticks", "`` `", 3, false},
		{"tildes", "~~~", 3, false},
		{"non-ASCII whitespace", "```\u00a0", 3, false},
		{"long fence", strings.Repeat("`", 4096), 4096, true},
		{"long but short fence", strings.Repeat("`", 4095), 4096, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMermaidFenceClose(tc.line, tc.length, '`'); got != tc.want {
				t.Fatalf("isMermaidFenceClose(%q, %d) = %v, want %v", tc.line, tc.length, got, tc.want)
			}
		})
	}
}

func BenchmarkMermaidTransformFencedBlocks(b *testing.B) {
	transform := createMermaidMarkdownTransformer(func() string { return "streaming" }, nil)
	ctx := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 80}
	markdown := strings.Repeat("```mermaid\nflowchart LR\n  A --> B\n```\n\n", 32)
	b.ReportAllocs()
	b.SetBytes(int64(len(markdown)))
	for b.Loop() {
		_ = transform(markdown, ctx)
	}
}
