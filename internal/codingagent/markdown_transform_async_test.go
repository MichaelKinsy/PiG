package codingagent

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi applies the whole transformer chain before Markdown parses or paints it (components/markdown-transform.ts:18-29). A slow subprocess must not expose raw or half-transformed Markdown or block the owner loop.
func TestMarkdownTransformFirstPaintWaitsForWholeChain(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &InteractiveMode{backgroundCtx: ctx, tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	m.newRunner = inproc.NewRunner([]extension.Extension{{MarkdownTransformer: func(text string, _ extension.MarkdownTransformContext) string {
		close(started)
		<-release
		return strings.ReplaceAll(text, "private", "redacted")
	}}, {MarkdownTransformer: func(text string, _ extension.MarkdownTransformContext) string {
		return "FINAL " + text
	}}}, "")
	block := m.newUserMessageBlock("private")
	chat := tui.NewContainer()
	chat.Add(block)
	rendered := make(chan []string, 1)
	go func() { rendered <- chat.Render(80) }()
	<-started
	blocked := false
	select {
	case first := <-rendered:
		if strings.Contains(strings.Join(first, "\n"), "private") {
			t.Error("untransformed Markdown reached the first paint")
		}
	case <-time.After(time.Second):
		blocked = true
		t.Error("transform blocked the render loop")
	}
	close(release)
	if blocked {
		<-rendered
	}
	m.backgroundTasks.Wait()
	if got := strings.Join(chat.Render(80), "\n"); !strings.Contains(got, "FINAL redacted") || strings.Contains(got, "private") {
		t.Fatalf("completed chain not painted: %q", got)
	}
}
