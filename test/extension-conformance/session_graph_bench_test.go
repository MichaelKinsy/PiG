package extensionconformance

import (
	"fmt"
	"sort"
	"testing"
)

// BenchmarkSessionGraphExtensionRunner times the extension_runner row's objects from inside each Go realization of the fixture (the loop runs in the extension, so a result is the SDK's cost of the operation with the host call it makes, not the cost of starting the cell): runner creation over two loaded extensions, one event emitted through the runner to the handlers of both, and a loader that loads and starts both extensions (the child extension spawn). Each cell reports "ns/loop-op", the in-extension loop's mean; the framework's ns/op also holds the one-time setup of a run (model runtime, first load, session manager) and is not the figure to read. Run with -benchtime=Nx: a spawn is slow (two Node processes).
func BenchmarkSessionGraphExtensionRunner(b *testing.B) {
	loaders := map[string]sessionGraphLoader{}
	for _, name := range []string{"go-fused", "go-strict"} {
		loaders[name] = sessionGraphSDKs()[name]
	}
	for name, load := range sessionGraphPackedSDKs() {
		if name == "go-packed" {
			loaders[name] = load
		}
	}
	names := make([]string, 0, len(loaders))
	for name := range loaders {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, op := range []string{"create", "emit", "spawn"} {
		for _, name := range names {
			b.Run(fmt.Sprintf("%s/%s", op, name), func(b *testing.B) {
				r := newSessionGraphRig(b)
				loaders[name](b, r)
				b.ReportAllocs()
				report, _ := r.scriptWith("extension_runner_bench", map[string]any{"op": op, "n": float64(b.N)})
				ns, isNumber := report["nsPerOp"].(float64)
				if !isNumber {
					b.Fatalf("report = %v", report)
				}
				b.ReportMetric(ns, "ns/loop-op")
			})
		}
	}
}
