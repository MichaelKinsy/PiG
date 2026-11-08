package workers

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports packages/chord/test/bench/workers.ts as Go benchmarks, run by hand (`go test -run '^$' -bench . ./chord/nextnode/workers`;
// Pi's run is pinned to one CPU): a worker's cold start with shipped scripts (fork to `started`, fork to `ready`, the
// median over the runs) and the server's memory per idle worker (heap after a GC, 20 workers running idle).

// median is the middle of values.
func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

func BenchmarkWorkerColdStart(b *testing.B) {
	scripts, err := newPlugins(b, b.TempDir()).Scripts("worker", background)
	if err != nil {
		b.Fatal(err)
	}
	var started, ready []float64
	for run := range b.N {
		forked := time.Now()
		var times struct{ started, ready float64 }
		done := make(chan struct{})
		child, err := NewWorkerProcess(
			ForkOptions{Entry: fixtureEntry(b), Env: fixtureEnvironment("worker"), OnOutput: func(string, []byte) {}},
			ProcessEvents{
				Control: func(message FromWorker) {
					switch message.Kind {
					case FromWorkerStarted:
						times.started = float64(time.Since(forked).Microseconds()) / 1000
					case FromWorkerReady:
						times.ready = float64(time.Since(forked).Microseconds()) / 1000
						close(done)
					}
				},
				LinkLost:   func() {},
				LinkClosed: func() {},
				Exited:     func(Exit) {},
			},
		)
		if err != nil {
			b.Fatal(err)
		}
		child.Send(ToWorker{Kind: ToWorkerStart, Address: session(fmt.Sprintf("R%d", run)), Start: delta.JsonObjectOf(), Scripts: scripts, Lock: fmt.Sprintf("bench:%d:%d", os.Getpid(), run)})
		<-done
		started, ready = append(started, times.started), append(ready, times.ready)
		child.Kill()
		<-child.Exited()
	}
	b.ReportMetric(median(started), "ms/started")
	b.ReportMetric(median(ready), "ms/ready")
}

func BenchmarkServerHeapPerIdleWorker(b *testing.B) {
	const workers = 20
	for range b.N {
		f := newFleet(b, fleetOptions{})
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		var held []*host.Reference
		for index := range workers {
			held = append(held, f.get(fixtureControl, fmt.Sprintf("W%d", index)))
		}
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc))/workers/1024, "KiB/worker")
		for _, reference := range held {
			reference.Release()
		}
		_ = f.workers.Close(background)
		_ = f.server.Close(background)
	}
}
