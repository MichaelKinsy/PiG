package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// BenchmarkMarkdownTransformGeneration includes admission, worker completion and parsing, including an unbroken64KiB paragraph. Cached rendering is measured separately by BenchmarkAsyncMarkdownCached.
func BenchmarkMarkdownTransformGeneration(b *testing.B) {
	for _, size := range []int{0, 1024, 64 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			var workers sync.WaitGroup
			md := NewMarkdown(strings.Repeat("x", size))
			md.AsyncTransform = &AsyncMarkdownTransform{Context: b.Context(), Start: workers.Go, Prepare: func(text string, _ int) func(context.Context) string {
				return func(context.Context) string { return text }
			}}
			b.ReportAllocs()
			for b.Loop() {
				md.Invalidate()
				md.Render(80)
				workers.Wait()
				md.Render(80)
			}
		})
	}
}

func BenchmarkMarkdownTransformQueue(b *testing.B) {
	for _, size := range []int{0, 1024, 64 << 10} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			var workers sync.WaitGroup
			var state asyncMarkdownState
			var queue MarkdownTransformQueue
			options := &AsyncMarkdownTransform{Context: b.Context(), Start: workers.Go, Queue: &queue, Prepare: func(text string, _ int) func(context.Context) string {
				return func(context.Context) string { return text }
			}}
			input := markdownTransformInput{text: strings.Repeat("x", size), width: 80}
			b.ReportAllocs()
			for b.Loop() {
				input.revision++
				state.resolve(options, input)
				workers.Wait()
			}
		})
	}
}
