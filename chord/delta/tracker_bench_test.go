package delta_test

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/delta-benchmark/benchmark.worker.ts tracker scenarios (import, committed-read, draft-read, sparse, queue, sort, dense, unshift) and packages/chord/test/delta-traversal.bench.ts (cold traversal through a draft). Upstream drives each scenario in a Node worker and reports wall time and V8 heap; Go reports ns/op and allocations per scenario through testing.B. The retained-heap sampling, the watch-* replication scenarios (they need the durable watch fan-out) and the conversation-view and memory workers have no counterpart here.

const benchRows = 100_000

func benchDocument(size int) *delta.JsonObject {
	rows := make([]any, size)
	for index := range rows {
		rows[index] = delta.JsonObjectOf("id", float64(index), "value", float64(index%97), "payload", "payload")
	}
	return delta.JsonObjectOf("rows", rows)
}

func rowValues(rows *delta.Array) float64 {
	sum := 0.0
	for index := range rows.Len() {
		sum += rows.Object(index).Get("value").(float64)
	}
	return sum
}

func BenchmarkTrackerScenarios(b *testing.B) {
	scenarios := []struct {
		name   string
		reads  bool
		mutate func(rows *delta.Array, size int)
	}{
		{"draft-read", true, func(rows *delta.Array, size int) { _ = rowValues(rows) }},
		{"sparse", false, func(rows *delta.Array, size int) {
			for index := 0; index < size; index += 1_000 {
				_ = rows.Object(index).Set("value", float64(-index))
			}
		}},
		{"queue", false, func(rows *delta.Array, size int) {
			for index := range 1_000 {
				rows.Shift()
				_, _ = rows.Push(delta.JsonObjectOf("id", float64(size+index), "value", float64(index), "payload", "queue"))
			}
		}},
		{"sort", false, func(rows *delta.Array, size int) {
			rows.Sort(func(left, right any) bool {
				return left.(*delta.Object).Get("id").(float64) < right.(*delta.Object).Get("id").(float64)
			})
		}},
		{"dense", false, func(rows *delta.Array, size int) {
			for index := range size {
				_ = rows.Object(index).Set("value", float64(-index))
			}
		}},
		{"unshift", false, func(rows *delta.Array, size int) {
			inserted := make([]any, 100_000)
			for id := range inserted {
				inserted[id] = delta.JsonObjectOf("id", float64(-id), "value", float64(id), "payload", "inserted")
			}
			_, _ = rows.Unshift(inserted...)
		}},
	}
	for _, scenario := range scenarios {
		b.Run(scenario.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				document := benchDocument(benchRows)
				if scenario.name == "sort" {
					rows := document.Value("rows").([]any)
					for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
						rows[left], rows[right] = rows[right], rows[left]
					}
				}
				tracker := delta.Track(document)
				b.StartTimer()
				change := tracker.BeginChange()
				scenario.mutate(change.State().Array("rows"), benchRows)
				prepared, err := change.Prepare()
				if err != nil {
					b.Fatal(err)
				}
				if err := tracker.Adopt(prepared); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	// Track adopts the revision without copying or walking it, so one document serves every iteration. Building a
	// document per iteration outside the timer made b.N grow until the untimed setup ran for hours.
	b.Run("import", func(b *testing.B) {
		document := benchDocument(benchRows)
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			delta.Track(document)
		}
	})
	b.Run("committed-read", func(b *testing.B) {
		tracker := delta.Track(benchDocument(benchRows))
		b.ReportAllocs()
		b.ResetTimer()
		for range b.N {
			sum := 0.0
			for _, row := range tracker.Value().Value("rows").([]any) {
				sum += row.(*delta.JsonObject).Value("value").(float64)
			}
			if sum < 0 {
				b.Fatal(fmt.Sprint(sum))
			}
		}
	})
}
