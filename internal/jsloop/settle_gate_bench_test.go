package jsloop

import (
	"sync"
	"testing"
)

// BenchmarkSettleGateBatch is one batch of eight calls: a gate, a slot per call, each call settled and run on its own goroutine, as a parallel tool batch does.
func BenchmarkSettleGateBatch(b *testing.B) {
	const calls = 8
	b.ReportAllocs()
	for b.Loop() {
		var gate SettleGate
		var wg sync.WaitGroup
		for range calls {
			slot := gate.NewSlot()
			wg.Go(func() {
				defer slot.Close()
				slot.Settle()
				slot.Run(func() {})
			})
		}
		wg.Wait()
	}
}

// BenchmarkSettleGateRunUncontended is the cost of a slot that settles and runs with nothing ahead of it.
func BenchmarkSettleGateRunUncontended(b *testing.B) {
	var gate SettleGate
	b.ReportAllocs()
	for b.Loop() {
		slot := gate.NewSlot()
		slot.Settle()
		slot.Run(func() {})
	}
}
