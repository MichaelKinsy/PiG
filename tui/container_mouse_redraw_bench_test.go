package tui

import (
	"fmt"
	"testing"
)

// clickRedrawFrame builds a transcript of n custom messages (each a MouseRegion card whose click flips a field), renders it once, and returns the
// root, the click that hits message index and the draw.
func clickRedrawFrame(n, index int) (root *Container, click TuiMouseEvent, open *bool) {
	root = NewContainer()
	for i := range n {
		state := false
		if i == index {
			open = &state
		}
		renderer := func(*CustomMessage, MessageRenderOptions) Component {
			return NewMouseRegion(&silentLine{line: func() string { return fmt.Sprintf("card %d open=%t", i, state) }}, func(event TuiMouseEvent) *TuiMouseEventResult {
				if event.Type != MouseClick {
					return nil
				}
				state = !state
				return &TuiMouseEventResult{Handled: true}
			})
		}
		root.Add(NewCustomMessageComponent(&CustomMessage{CustomType: "card", Content: "x"}, renderer, nil, 1, nil, "", ""))
	}
	root.Render(100)
	rows := index*2 + 1 // each message is a spacer row and the card row
	click = TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft, X: 3, Y: rows, ScreenX: 3, ScreenY: rows, Width: 100, Height: n * 2}
	return root, click, open
}

// BenchmarkTranscriptClickRedraw is the cost of the frame after a click on one message of a long transcript: the dispatch through the containers and the
// render that follows. BenchmarkTranscriptSetExpandedRedraw is the same frame after the built-in toggle (SetExpanded on that message), the path a
// compaction or skill card takes, and BenchmarkTranscriptCachedRender the frame with nothing changed.
func BenchmarkTranscriptClickRedraw(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("messages=%d", n), func(b *testing.B) {
			root, click, _ := clickRedrawFrame(n, n/2)
			b.ReportAllocs()
			for b.Loop() {
				if DispatchMouseEvent(root, click) == nil {
					b.Fatal("click not handled")
				}
				root.Render(100)
			}
		})
	}
}

func BenchmarkTranscriptSetExpandedRedraw(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("messages=%d", n), func(b *testing.B) {
			root := NewContainer()
			var target *CustomMessageComponent
			for i := range n {
				component := NewCustomMessageComponent(&CustomMessage{CustomType: "card", Content: "x"}, func(*CustomMessage, MessageRenderOptions) Component {
					return &silentLine{line: func() string { return fmt.Sprintf("card %d", i) }}
				}, nil, 1, nil, "", "")
				if i == n/2 {
					target = component
				}
				root.Add(component)
			}
			root.Render(100)
			expanded := false
			b.ReportAllocs()
			for b.Loop() {
				expanded = !expanded
				target.SetExpanded(expanded)
				root.Render(100)
			}
		})
	}
}

func BenchmarkTranscriptCachedRender(b *testing.B) {
	for _, n := range []int{100, 1000} {
		b.Run(fmt.Sprintf("messages=%d", n), func(b *testing.B) {
			root, _, _ := clickRedrawFrame(n, n/2)
			b.ReportAllocs()
			for b.Loop() {
				root.Render(100)
			}
		})
	}
}
