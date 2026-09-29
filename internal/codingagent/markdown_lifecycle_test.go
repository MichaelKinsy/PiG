package codingagent

import (
	"context"
	"io"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestMarkdownModeClearRemovesQueuedBodies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started, release := make(chan struct{}), make(chan struct{})
		var mu sync.Mutex
		var calls []string
		m := &InteractiveMode{backgroundCtx: ctx, chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
		m.newRunner = inproc.NewRunner([]extension.Extension{{MarkdownTransformer: func(text string, c extension.MarkdownTransformContext) string {
			mu.Lock()
			calls = append(calls, text)
			mu.Unlock()
			if text == "active" {
				close(started)
				<-release
				if c.Context.Err() != nil {
					t.Error("ordinary clear cancelled an admitted callback's Mode lifetime")
				}
			}
			return "complete " + text
		}}}, "")
		m.chatContainer.Add(m.newUserMessageBlock("active"))
		m.chatContainer.Render(80)
		<-started
		m.chatContainer.Add(m.newUserMessageBlock("queued"))
		m.chatContainer.Render(80)
		m.buildSlashContext(ctx).Clear()
		close(release)
		m.backgroundTasks.Wait()
		mu.Lock()
		got := slices.Clone(calls)
		mu.Unlock()
		if !slices.Equal(got, []string{"active"}) {
			t.Fatalf("removed queued bodies executed: %q", got)
		}
		if len(m.markdownBlocks) != 0 || len(m.chatContainer.Render(80)) != 0 {
			t.Fatal("clear retained removed Markdown components")
		}
	})
}

func TestMarkdownModeShutdownCancelsInvocationLifetime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		started := make(chan struct{})
		m := &InteractiveMode{backgroundCtx: ctx, tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
		m.newRunner = inproc.NewRunner([]extension.Extension{{MarkdownTransformer: func(text string, c extension.MarkdownTransformContext) string {
			close(started)
			<-c.Context.Done()
			return "late"
		}}}, "")
		block := m.newAssistantMessageBlock()
		block.SetContent([]tui.AssistantSegment{{Text: "original"}})
		block.Render(80)
		<-started
		cancel()
		m.disposeMarkdownBlocks()
		m.backgroundTasks.Wait()
		if got := block.Render(80); len(got) != 1 || got[0] != "\x1b]133;B\x07\x1b]133;C\x07\x1b]133;A\x07" {
			t.Fatalf("owner cancellation published content: %q", got)
		}
	})
}
