package tui

import (
	"fmt"
	"strings"
	"testing"
)

// benchConversationRoot builds a representative fullscreen transcript: a
// ScrollView over a VStack of many multi-line message blocks, sized like a real
// session. This exercises the layout recursion, the per-frame render cache, the
// scroll viewport, and the composite paint path.
func benchConversationRoot(messages int) *ScrollView {
	children := make([]StackChild, 0, messages)
	for i := range messages {
		body := make([]string, 0, 6)
		body = append(body, fmt.Sprintf("user: message %d", i))
		for line := range 4 {
			body = append(body, "  "+strings.Repeat("lorem ipsum ", 6)+fmt.Sprintf("(%d.%d)", i, line))
		}
		children = append(children, StackChild{Component: &stubComponent{lines: body}})
	}
	content := NewVStack(children, StackOptions{})
	return NewScrollView(content, ScrollViewOptions{Follow: "end", Scrollbar: "auto"})
}

func BenchmarkRenderLayoutFrame(b *testing.B) {
	root := benchConversationRoot(40)
	b.Cleanup(root.Dispose)
	// Warm one frame so the ScrollView has measured content.
	RenderLayoutFrame(root, 100, 40, noRender)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = RenderLayoutFrame(root, 100, 40, noRender)
	}
}
