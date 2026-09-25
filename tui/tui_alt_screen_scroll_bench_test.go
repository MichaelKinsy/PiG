package tui

import (
	"fmt"
	"io"
	"testing"
)

// newScrollTranscriptAltScreen builds a fullscreen alt screen shaped like the
// interactive chat viewport: a primary transcript ScrollView (auto scrollbar,
// chained overscroll) over a production-shaped Document(header, chatContainer)
// with styled Markdown messages, above a fixed dock line. Every message is
// distinct, so no cross-message dedupe can hide per-line work.
func newScrollTranscriptAltScreen(messages int) (*TuiAltScreen, *ScrollView) {
	chat := NewContainer()
	for i := range messages {
		chat.Add(NewMarkdown(fmt.Sprintf("**user %d**: please review `file%d.go`\n\n", i, i) + streamingBody(1200+i%7*40)))
	}
	document := NewContainer(NewText("transcript header"), chat)
	transcript := NewScrollView(document, ScrollViewOptions{Follow: "end", Primary: true, Overscroll: "chain", Scrollbar: "auto"})
	root := NewVStack([]StackChild{
		{Component: transcript, StackEntryOptions: StackEntryOptions{Basis: new(0), Grow: new(1), Shrink: new(1), MinSize: new(1)}},
		{Component: NewText("\x1b[2m> editor dock\x1b[0m"), StackEntryOptions: StackEntryOptions{Grow: new(0), Shrink: new(1), MinSize: new(1)}},
	}, StackOptions{})
	tui := newAltScreenForTest(nil, 120, 40, TuiAltScreenOptions{})
	tui.out = io.Discard
	tui.SetLayoutRoot(root)
	tui.Start()
	tui.doRender()
	return tui, transcript
}

// BenchmarkAltScreenPageUpScroll measures one fullscreen PageUp frame over a
// long transcript: the viewport origin changes every frame, so every visible
// row is repainted and the scrollbar is active. At the top it jumps back to
// the end, like a user paging through history repeatedly.
func BenchmarkAltScreenPageUpScroll(b *testing.B) {
	for _, messages := range []int{270, 1000} {
		b.Run(fmt.Sprintf("messages-%d", messages), func(b *testing.B) {
			tui, transcript := newScrollTranscriptAltScreen(messages)
			b.Cleanup(transcript.Dispose)
			page := transcript.ViewportHeight() - 1
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if transcript.ScrollBy(-page) == -page {
					transcript.ScrollToEnd()
				}
				tui.doRender()
			}
		})
	}
}
