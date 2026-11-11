package driver_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

// iterationTB runs the cleanups of one benchmark iteration when the iteration ends, so a run of many iterations does not
// hold every harness it opened until the benchmark returns.
type iterationTB struct {
	testing.TB
	cleanups []func()
}

func (tb *iterationTB) Cleanup(f func()) { tb.cleanups = append(tb.cleanups, f) }

func (tb *iterationTB) close() {
	for _, v := range slices.Backward(tb.cleanups) {
		v()
	}
	tb.cleanups = nil
}

// coreFanout opens a harness whose one tool, batch, runs calls nested echo calls in parallel (Pi scheduler-fanout-bench.ts)
// and returns the operation that submits the input, waits for the run to settle and checks that every call succeeded.
func coreFanout(tb testing.TB, calls int) func() {
	tb.Helper()
	var failed atomic.Int64
	_, _, submit := nestedCoreHarnessPaused(tb,
		echoCoreTool(),
		nestedCoreTool("batch", func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			var wg sync.WaitGroup
			for range calls {
				wg.Go(func() {
					r, err := api.ExecuteTool(ctx, "echo", map[string]any{"text": "x"}, durable.NestedCallOptions{Progress: new(false)})
					if err != nil || r.IsError {
						failed.Add(1)
					}
				})
			}
			wg.Wait()
			return nestedCoreText("ok"), nil
		}),
	)
	return func() {
		sub := submit()
		ctx, cancel := context.WithTimeout(tb.Context(), 250*time.Second)
		defer cancel()
		settled, err := sub.Wait(ctx)
		if err != nil || settled.Status != durable.SubmissionDone || failed.Load() != 0 {
			panic(fmt.Sprintf("%d calls settled %+v, %v, %d failed", calls, settled, err, failed.Load()))
		}
	}
}

func BenchmarkCoreNestedCallFanout(b *testing.B) {
	for _, calls := range []int{100, 1000, 4000} {
		b.Run(fmt.Sprint(calls), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				b.StopTimer()
				tb := &iterationTB{TB: b}
				run := coreFanout(tb, calls)
				b.StartTimer()
				run()
				b.StopTimer()
				tb.close()
				b.StartTimer()
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(calls), "ns/call")
		})
	}
}
