//go:build !race

package tui

import (
	"fmt"
	"runtime"
	"testing"
)

// A transcript keeps one component per message for the life of the session, so what a rendered message retains must be a
// bounded multiple of its source and must not grow with the number of messages. Markdown keeps memos for the next
// streamed delta (the wraps, inline renderings and resume point), which only a streaming text needs; a message that
// arrives whole must drop them (Pi 54c19a25 frees the equivalent memory of its Markdown, which every transcript message
// keeps). The race detector inflates the heap, so the measurement runs without it.

func retentionHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func TestWholeMessageRetention(t *testing.T) {
	// bound is what an owner may retain per message of 1.2 KB. The rows are the floor: 29 padded, colored rows of 6.1 KB
	// that the Markdown caches (a boxed owner's Box copies them again), and a message keeps no memo for a next streamed
	// delta, which would add 12 KB to a user message and 6 KB to a boxed one.
	owners := []struct {
		name  string
		bound float64
		build func(src string) Component
	}{
		{"user message", 10 << 10, func(src string) Component { return NewUserMessageComponent(src, nil, 1, nil, "id", "ts") }},
		{"branch summary", boxedBound, func(src string) Component {
			c := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: src}, nil, 1)
			c.SetExpanded(true)
			return c
		}},
		{"compaction summary", boxedBound, func(src string) Component {
			c := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: src, TokensBefore: 1234}, nil, 1)
			c.SetExpanded(true)
			return c
		}},
		{"custom message", boxedBound, func(src string) Component {
			c := NewCustomMessageComponent(&CustomMessage{CustomType: "x", Content: src}, nil, nil, 1)
			c.SetExpanded(true)
			return c
		}},
		{"skill invocation", boxedBound, func(src string) Component {
			c := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "s", Content: src}, nil, 1)
			c.SetExpanded(true)
			return c
		}},
	}
	sizes := []int{10, 100, 1000, 10000}
	for _, owner := range owners {
		t.Run(owner.name, func(t *testing.T) {
			perMessage := make([]float64, len(sizes))
			for k, n := range sizes {
				src := make([]string, n)
				for i := range src {
					src[i] = retentionSource(i)
				}
				before := retentionHeap()
				comps := make([]Component, n)
				for i := range comps {
					comps[i] = owner.build(src[i])
					comps[i].Render(100)
				}
				after := retentionHeap()
				perMessage[k] = float64(after-before) / float64(n)
				t.Logf("%s n=%d: %.0f B retained per message of %d B", owner.name, n, perMessage[k], len(src[0]))
				runtime.KeepAlive(comps)
				if perMessage[k] > owner.bound {
					t.Errorf("n=%d: %.0f B retained per message, want at most %.0f", n, perMessage[k], owner.bound)
				}
			}
			if small, large := perMessage[1], perMessage[3]; large > small*1.2 {
				t.Errorf("retention per message grows with the transcript: %.0f B at n=100, %.0f B at n=10000", small, large)
			}
		})
	}
}

// BenchmarkUserMessageRetention reports the heap a rendered user message keeps, with n messages in the transcript.
func BenchmarkUserMessageRetention(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("messages=%d", n), func(b *testing.B) {
			src := make([]string, n)
			for i := range src {
				src[i] = retentionSource(i)
			}
			b.ReportAllocs()
			var kept float64
			for b.Loop() {
				before := retentionHeap()
				comps := make([]Component, n)
				for i := range comps {
					u := NewUserMessageComponent(src[i], nil, 1, nil, "id", "ts")
					u.Render(100)
					comps[i] = u
				}
				kept = float64(retentionHeap()-before) / float64(n)
				runtime.KeepAlive(comps)
			}
			b.ReportMetric(kept, "retained-B/msg")
		})
	}
}

const boxedBound = 19 << 10
