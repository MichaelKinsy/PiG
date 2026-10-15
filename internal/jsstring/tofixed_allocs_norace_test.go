//go:build !race

package jsstring

import "testing"

// A value that is not a tie costs the one string, not big-number arithmetic.
func TestToFixedAllocatesOnceForAnOrdinaryCost(t *testing.T) {
	if got := testing.AllocsPerRun(100, func() { _ = ToFixed(0.123456, 4) }); got > 1 {
		t.Fatalf("ToFixed(0.123456, 4) allocates %v times, want at most 1", got)
	}
}

func BenchmarkToFixed(b *testing.B) {
	for _, c := range []struct {
		name   string
		x      float64
		digits int
	}{{"cost", 0.123456, 4}, {"tie", 0.125, 2}, {"large", 1234567.891, 2}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = ToFixed(c.x, c.digits)
			}
		})
	}
}
