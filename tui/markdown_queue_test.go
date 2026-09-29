package tui

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

func TestMarkdownQueueAdmissionAndReplacementOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input []string
		want  []string
	}{
		{"A1-A2-B1", []string{"A2", "B1"}, []string{"A1", "A2", "B1"}},
		{"A1-B1-A2", []string{"B1", "A2"}, []string{"A1", "B1", "A2"}},
		{"replace-at-tail", []string{"A2", "B1", "A3"}, []string{"A1", "B1", "A3"}},
		{"A-B-A", []string{"B1", "A2", "A1"}, []string{"A1", "B1", "A1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var workers sync.WaitGroup
				var queue MarkdownTransformQueue
				var mu sync.Mutex
				var trace []string
				started, release := make(chan struct{}), make(chan struct{})
				options := &AsyncMarkdownTransform{Context: t.Context(), Start: workers.Go, Queue: &queue, Prepare: func(text string, _ int) func(context.Context) string {
					return func(context.Context) string {
						mu.Lock()
						first := len(trace) == 0
						trace = append(trace, text)
						mu.Unlock()
						if first {
							close(started)
							<-release
						}
						return text
					}
				}}
				a, b := NewMarkdown("A1"), NewMarkdown("B1")
				a.AsyncTransform, b.AsyncTransform = options, options
				a.Render(80)
				<-started
				for _, input := range tc.input {
					md := a
					if input == "B1" {
						md = b
					}
					md.Content = input
					md.Render(80)
				}
				synctest.Wait()
				mu.Lock()
				before := slices.Clone(trace)
				mu.Unlock()
				close(release)
				workers.Wait()
				if !slices.Equal(before, []string{"A1"}) {
					t.Errorf("callback overlap: %q", before)
				}
				if !slices.Equal(trace, tc.want) {
					t.Fatalf("admission order=%q; want %q", trace, tc.want)
				}
				queue.mu.Lock()
				defer queue.mu.Unlock()
				if queue.running || queue.jobs.Len() != 0 || len(queue.pending) != 0 {
					t.Fatal("completed queue retains active work")
				}
			})
		})
	}
}
