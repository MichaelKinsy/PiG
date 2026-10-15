package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// Pi assistant-message.ts:91-116 creates fresh Markdown components for each content update, including equal text; stateful transforms must run again.
func TestAssistantEqualContentRerunsMarkdownTransformer(t *testing.T) {
	var workers sync.WaitGroup
	var calls atomic.Int32
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	block.SetAsyncMarkdownTransforms(&AsyncMarkdownTransform{Context: t.Context(), Start: workers.Go, Prepare: func(text string, _ int) func(context.Context) string {
		return func(context.Context) string { return fmt.Sprintf("%s version%d", text, calls.Add(1)) }
	}}, nil)
	for i := 1; i <= 2; i++ {
		block.SetContent([]AssistantSegment{{Text: "same"}})
		block.Render(80)
		workers.Wait()
		if got := strings.Join(block.Render(80), "\n"); !strings.Contains(got, fmt.Sprintf("same version%d", i)) {
			t.Fatalf("replacement %d reused transformed text: %q", i, got)
		}
	}
}

func TestAsyncMarkdownOptionsReplacementRepaintsEqualText(t *testing.T) {
	var workers sync.WaitGroup
	md := NewMarkdown("same")
	for _, prefix := range []string{"first", "second"} {
		md.AsyncTransform = &AsyncMarkdownTransform{Context: t.Context(), Start: workers.Go, Prepare: func(text string, _ int) func(context.Context) string {
			return func(context.Context) string { return prefix + " " + text }
		}}
		md.Render(80)
		workers.Wait()
		want := prefix + " same"
		want += strings.Repeat(" ", 80-len(want))
		if got := strings.Join(md.Render(80), "\n"); got != want {
			t.Fatalf("options replacement retained old frame: %q", got)
		}
	}
}

func TestDroppedAssistantMarkdownCancelsGeneration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var workers sync.WaitGroup
		started, release := make(chan struct{}), make(chan struct{})
		var cancelled bool
		block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		block.SetAsyncMarkdownTransforms(&AsyncMarkdownTransform{Context: t.Context(), Start: workers.Go, Prepare: func(string, int) func(context.Context) string {
			return func(ctx context.Context) string {
				close(started)
				<-release
				cancelled = ctx.Err() != nil
				return "obsolete"
			}
		}}, nil)
		block.SetContent([]AssistantSegment{{Text: "removed"}})
		block.Render(80)
		<-started
		block.SetContent(nil)
		close(release)
		workers.Wait()
		if !cancelled {
			t.Fatal("removed segment still owns a live generation")
		}
		if got := block.Render(80); len(got) != 0 {
			t.Fatalf("removed body painted: %q", got)
		}
	})
}
