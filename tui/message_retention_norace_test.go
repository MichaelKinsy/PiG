//go:build !race

package tui

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// A transcript keeps one component per message for the life of the session, so what a rendered message retains must be a
// bounded multiple of its source and must not grow with the number of messages. Markdown keeps memos for the next
// streamed delta (the wraps, inline renderings and resume point), which only a streaming text needs; a message that
// arrives whole must drop them (Pi 54c19a25 frees the equivalent memory of its Markdown, which every transcript message
// keeps). The race detector inflates the heap, so the measurement runs without it.
//
// The readings are the heap of the process, which counts every goroutine in it, not only the messages: what an earlier test left
// running, or has not released yet, moves a reading by as much as the few KB a message retains (at n=10 a foreign 100 KB is 10 KB
// per message, the whole bound of a user message). The test runs in a process of its own (testenv.InOwnProcess), where nothing but
// its messages allocates.

func retentionHeap() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func TestWholeMessageRetention(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
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
			c := NewBranchSummaryMessageComponent(BranchSummaryMessage{Summary: src}, nil, 1, nil, "", "")
			c.SetExpanded(true)
			return c
		}},
		{"compaction summary", boxedBound, func(src string) Component {
			c := NewCompactionSummaryMessageComponent(CompactionSummaryMessage{Summary: src, TokensBefore: 1234}, nil, 1, nil, "", "")
			c.SetExpanded(true)
			return c
		}},
		{"custom message", boxedBound, func(src string) Component {
			c := NewCustomMessageComponent(&CustomMessage{CustomType: "x", Content: src}, nil, nil, 1, nil, "", "")
			c.SetExpanded(true)
			return c
		}},
		{"skill invocation", boxedBound, func(src string) Component {
			c := NewSkillInvocationMessageComponent(ParsedSkillBlock{Name: "s", Content: src}, nil, 1, nil, "", "")
			c.SetExpanded(true)
			return c
		}},
	}
	sizes := []int{10, 100, 1000, 10000}
	for _, owner := range owners {
		t.Run(owner.name, func(t *testing.T) {
			measure := func(n int) float64 {
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
				runtime.KeepAlive(comps)
				return float64(after-before) / float64(n)
			}
			// The first reading of a process is not the cost of a message. A process that has built no message yet loads what
			// every message shares (the grammar of the code block, the width tables: 680 KB, about 68 KB per message at n=10), and
			// its first measured windows read another 5.6 or 11 KB of runtime state that later ones do not (fresh processes at
			// n=10: 9146, 9703 or 10261 B per message, the bound 10240, 9160 once settled). In the package's process earlier tests
			// had built messages and collected many times, so the readings were settled; here one window of 100 messages is run
			// and discarded, and the readings below are the settled ones.
			measure(100)
			perMessage := make([]float64, len(sizes))
			for k, n := range sizes {
				perMessage[k] = measure(n)
				t.Logf("%s n=%d: %.0f B retained per message of %d B", owner.name, n, perMessage[k], len(retentionSource(0)))
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
