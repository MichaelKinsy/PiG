// Benchmarks streaming one answer (a text and a tool call of as many bytes) through partial commits, the workload of
// harness-view-run.test.ts "commits a growing text and tool call at a cost that follows the new text, not the string lengths".
// ns/op, B/op and allocs/op are per answer; commits/op is the partial commits one answer makes.

package harness

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

func BenchmarkStreamedPartials(b *testing.B) {
	for _, size := range []int{40_000, 400_000} {
		b.Run(fmt.Sprintf("%dKB", size/1000), func(b *testing.B) {
			var commits atomic.Int64
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				setup := streamedTextAndCall(b, size, 64, 64_000)
				harness, root := openChat(b, storage.NewMemoryStorage(), setup)
				followCommits(harness, func(context.Context, session.ObservedCommit) { commits.Add(1) })
				b.StartTimer()
				submission := submit(b, root, inputDraft("go"))
				must(submission.Wait(testContext))
				b.StopTimer()
				closeHarness(b, harness)
			}
			b.ReportMetric(float64(commits.Load())/float64(b.N), "commits/op")
		})
	}
}
