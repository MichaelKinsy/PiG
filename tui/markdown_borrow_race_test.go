package tui

import (
	"sync"
	"testing"
)

// Public Renders of one Markdown from several goroutines only read it on a cache hit, also after a container borrowed its rows.
func TestConcurrentPublicRenderOfABorrowedMarkdown(t *testing.T) {
	md := NewMarkdown("## T\n\nsome **text** that is long enough to wrap at a narrow width, more more more.\n\n- a\n- b\n")
	chat := NewContainer()
	chat.Add(md)
	chat.Render(40)
	chat.Render(40)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 200 {
				_ = md.Render(40)
			}
		})
	}
	wg.Wait()
}
