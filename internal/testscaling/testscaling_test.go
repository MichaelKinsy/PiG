package testscaling

import (
	"strings"
	"testing"
)

var sink [][]byte

// grow allocates n buffers whose sizes sum to n*width: linear in n for a fixed width.
func grow(n, width int) {
	sink = sink[:0]
	for range n {
		sink = append(sink, make([]byte, width))
	}
}

// The gate must pass a path at its bound and fail a path one order worse: a quadratic path declared linear, a linear path declared constant.
func TestViolationsSeparateOrders(t *testing.T) {
	linear := Run(Case{Name: "linear", Size: 200, Bound: Linear, Prepare: func(n int) func() { return func() { grow(n, 64) } }})
	quadratic := Run(Case{Name: "quadratic", Size: 200, Bound: Linear, Prepare: func(n int) func() { return func() { grow(n, 64*n) } }})
	constant := Run(Case{Name: "constant", Size: 200, Bound: Constant, Prepare: func(int) func() { return func() { grow(8, 64) } }})
	if v := linear.Violations(Linear); len(v) != 0 {
		t.Fatalf("a linear path failed its linear bound: %v", v)
	}
	if v := constant.Violations(Constant); len(v) != 0 {
		t.Fatalf("a constant path failed its constant bound: %v", v)
	}
	if v := quadratic.Violations(Linear); len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), "allocated bytes grew") {
		t.Fatalf("a quadratic path passed a linear bound: slopes allocs %.2f bytes %.2f", quadratic.AllocsSlope, quadratic.BytesSlope)
	}
	if v := linear.Violations(Constant); len(v) == 0 {
		t.Fatalf("a linear path passed a constant bound: slopes allocs %.2f bytes %.2f", linear.AllocsSlope, linear.BytesSlope)
	}
	if v := quadratic.Violations(Quadratic); len(v) != 0 {
		t.Fatalf("a quadratic path failed its quadratic bound: %v", v)
	}
}

func TestSlopeFloorsAnEmptyMeasurement(t *testing.T) {
	if got := Slope(0, 0, 4); got != 0 {
		t.Fatalf("Slope(0, 0) = %v, want 0", got)
	}
	if got := Slope(0, 40, 4); got != 1 {
		t.Fatalf("Slope(0, 40) with floor 4 = %v, want 1", got)
	}
}

// A bytes bound separate from the allocation bound passes a path whose bytes grow a little faster than linearly and still
// fails one whose bytes are quadratic, while the allocation count keeps the declared bound.
func TestBytesBoundSeparatesTheByteSlope(t *testing.T) {
	// n frames each copying a slice of one pointer per frame so far: allocations grow linearly, bytes quadratically.
	copies := func(n int) func() {
		return func() {
			sink = sink[:0]
			for frame := range n {
				sink = append(sink, make([]byte, 8*frame+8))
			}
		}
	}
	quadraticBytes := Run(Case{Name: "frame copy", Size: 200, Bound: Linear, Prepare: copies})
	if v := quadraticBytes.ViolationsWith(Linear, Linear); len(v) == 0 {
		t.Fatalf("quadratic bytes passed a linear bytes bound: slopes allocs %.2f bytes %.2f", quadraticBytes.AllocsSlope, quadraticBytes.BytesSlope)
	}
	if v := quadraticBytes.ViolationsWith(Linear, Quadratic); len(v) != 0 {
		t.Fatalf("quadratic bytes failed a quadratic bytes bound: %v", v)
	}
	if v := quadraticBytes.ViolationsWith(Constant, Quadratic); len(v) == 0 || strings.Contains(strings.Join(v, "\n"), "allocated bytes") {
		t.Fatalf("the allocation bound was not enforced on its own: %v", v)
	}
}
