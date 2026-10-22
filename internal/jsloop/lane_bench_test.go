package jsloop

import "testing"

// BenchmarkLaneReserveWaitRelease is one call's trip through a lane: reserve its place, wait for the earlier place, release.
func BenchmarkLaneReserveWaitRelease(b *testing.B) {
	var lane Lane
	b.ReportAllocs()
	for b.Loop() {
		place := lane.Reserve()
		place.Wait()
		place.Release()
	}
}
