package tui

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestAsyncMarkdownReplacesPendingGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var workers sync.WaitGroup
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	md := NewMarkdown("first")
	md.AsyncTransform = &AsyncMarkdownTransform{
		Context: ctx, Start: workers.Go,
		Prepare: func(text string, _ int) func(context.Context) string {
			return func(ctx context.Context) string {
				if calls.Add(1) == 1 {
					close(started)
					<-release
					if ctx.Err() == nil {
						t.Error("superseded generation was not cancelled")
					}
					return "OBSOLETE"
				}
				return "transformed " + text
			}
		},
	}
	if lines := md.Render(80); len(lines) != 0 {
		t.Fatalf("unprepared text was painted: %q", lines)
	}
	<-started
	for i := range 1000 {
		md.Content = strconv.Itoa(i)
		if lines := md.Render(80); len(lines) != 0 {
			t.Fatalf("pending text was painted: %q", lines)
		}
	}
	close(release)
	workers.Wait()
	if calls.Load() != 2 {
		t.Fatalf("want only the active and last pending generation, got %d calls", calls.Load())
	}
	// Markdown.render pads the completed row to the requested width (markdown.ts:328-341).
	if got := strings.Join(md.Render(80), "\n"); got != "transformed 999"+strings.Repeat(" ", 80-len("transformed 999")) {
		t.Fatalf("late generation overwrote the final result: %q", got)
	}
}

func TestAsyncMarkdownOwnerCancellationDrains(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	started := make(chan struct{})
	var paints atomic.Int32
	md := NewMarkdown("raw")
	md.AsyncTransform = &AsyncMarkdownTransform{
		Context: ctx, Start: workers.Go, Invalidate: func() { paints.Add(1) },
		Prepare: func(_ string, _ int) func(context.Context) string {
			return func(ctx context.Context) string {
				close(started)
				<-ctx.Done()
				return "late"
			}
		},
	}
	md.Render(80)
	<-started
	cancel()
	workers.Wait()
	if paints.Load() != 0 || len(md.Render(80)) != 0 {
		t.Fatal("cancelled owner published or scheduled a new frame")
	}
}

func BenchmarkAsyncMarkdownCached(b *testing.B) {
	var workers sync.WaitGroup
	md := NewMarkdown(strings.Repeat("ordinary streaming text ", 1000))
	md.AsyncTransform = &AsyncMarkdownTransform{
		Context: b.Context(), Start: workers.Go,
		Prepare: func(text string, _ int) func(context.Context) string {
			return func(context.Context) string { return text }
		},
	}
	md.Render(80)
	workers.Wait()
	md.Render(80)
	b.ReportAllocs()
	for b.Loop() {
		md.Render(80)
	}
}
