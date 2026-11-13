package codingagent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// writeCardRowsSource is TypeScript with what moves a wrapped row: tabs, wide and combining characters that fall on the wrap
// column at several widths, a block comment and a template literal whose colours cross lines, and lines wider than any width the
// test draws at.
func writeCardRowsSource() string {
	var source strings.Builder
	for n := 0; source.Len() < 24<<10; n++ {
		switch n % 9 {
		case 0:
			fmt.Fprintf(&source, "\texport function step%d(input: number[]): number { return input.reduce((a, b) => a + b * %d, %d); }\n", n, n%97, n)
		case 1:
			source.WriteString("/* a block comment that\n spans lines 漢字 and e\u0301 */\n")
		case 2:
			source.WriteString("const label = `template ")
			source.WriteString(strings.Repeat("漢", 40+n%11))
			source.WriteString("\n   continues ${n} here`;\n")
		case 3:
			source.WriteString(strings.Repeat("漢字e\u0301", 30+n%7) + "\n")
		case 4:
			source.WriteString("\t\tconst wide = \"" + strings.Repeat("x", 100+n%37) + "\"; // 😀 tail\n")
		default:
			fmt.Fprintf(&source, "export const value%d = %d; // %s\n", n, n, strings.Repeat("word ", n%13))
		}
	}
	return source.String()
}

// A write call's Text and the Box around it, drawn after every streamed update as the card draws them (the Box is cleared and
// refilled each time, the renderer reuses its Text), give the rows that a fresh Text and a fresh Box give for the same text: across
// the widths the terminal takes while the call streams, a card that is expanded and collapsed between updates, and the call's
// completion.
func TestWriteCardRowsMatchAFreshChainWhileStreaming(t *testing.T) {
	previous := writeWarmHighlight
	writeWarmHighlight = func(string, string) bool { return true }
	t.Cleanup(func() { writeWarmHighlight = previous })
	content := writeCardRowsSource()
	background := func(text string) string { return "\x1b[48;5;236m" + text + "\x1b[49m" }
	box := tui.NewPaddedBox(1, 1, background)
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work"}
	widths := []int{120, 61, 160, 33, 120, 79}
	const step = 713
	frames := 0
	draw := func(end int, complete bool) {
		quoted, err := json.Marshal(content[:end])
		if err != nil {
			t.Fatal(err)
		}
		context.ArgsComplete = complete
		component := writeRenderCall(json.RawMessage(`{"path":"big/source.ts","content":`+string(quoted)+`}`), nil, context)
		context.LastComponent = component
		text, ok := component.(*tui.Text)
		if !ok {
			t.Fatalf("the write renderer returned %T, not a Text", component)
		}
		width := widths[min(frames*len(widths)/(len(content)/step+2), len(widths)-1)]
		box.Clear()
		box.AddChild(tui.NewMouseRegion(text, nil))
		got := box.Render(width)
		fresh := tui.NewPaddedBox(1, 1, background)
		fresh.AddChild(tui.NewMouseRegion(tui.NewPaddedText(text.Content, 0, 0, nil), nil))
		if want := fresh.Render(width); !slices.Equal(got, want) {
			t.Fatalf("frame %d (%d bytes, width %d, expanded %v, complete %v) differs from a fresh Text and Box: %d rows, want %d", frames, end, width, context.Expanded, complete, len(got), len(want))
		}
		frames++
	}
	for end := step; end < len(content); end += step {
		// Expanded for the middle fifth of the stream: every row of the file is drawn, and the card collapses again.
		context.Expanded = end > len(content)*2/5 && end < len(content)*3/5
		draw(end, false)
	}
	context.Expanded = false
	draw(len(content), true)
	context.Expanded = true
	draw(len(content), true)
	if frames < 30 {
		t.Fatalf("only %d frames were drawn", frames)
	}
}
