package subprocess

import (
	"fmt"
	"sync"
	"testing"
)

// BenchmarkCallLanesPush is the cost the host pays to queue one extension→host call on a lane and run it on the lane's drain goroutine, before the
// call's own work: one lane, so every call but the first waits behind the call before it.
func BenchmarkCallLanesPush(b *testing.B) {
	lanes := newCallLanes()
	var done sync.WaitGroup
	b.ReportAllocs()
	for b.Loop() {
		done.Add(1)
		lanes.push("lane", done.Done)
	}
	done.Wait()
}

// BenchmarkCallLanesEightLanes is BenchmarkCallLanesPush over eight lanes in turn, the calls of the requests of one parallel batch.
func BenchmarkCallLanesEightLanes(b *testing.B) {
	lanes := newCallLanes()
	names := make([]string, 8)
	for i := range names {
		names[i] = fmt.Sprintf("request-%d", i)
	}
	var done sync.WaitGroup
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		done.Add(1)
		lanes.push(names[i%len(names)], done.Done)
		i++
	}
	done.Wait()
}
