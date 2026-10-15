// SPDX-License-Identifier: MIT

package perfgate

import (
	"math"
	"testing"
)

// Scaling is the heap allocation of one operation at three sizes a decade apart: N, 10N and 100N.
type Scaling struct {
	Sizes [3]int
	Cost  [3]Work
}

// scalingRepeats is how often each size is measured. The count is process-wide, so a runtime or test-framework goroutine that allocates during a measurement adds to it, and an allocation of the operation itself never subtracts: the smallest of the repeats is the operation's own cost.
const scalingRepeats = 3

// MeasureScaling measures the operation at each size. prepare builds the state of one size (not measured) and returns the one operation whose allocation is counted; the operation must leave that state as it found it, because it runs once to warm caches and then scalingRepeats times under [Measure]. Like Measure, the count is process-wide, so the caller keeps other goroutines idle.
func MeasureScaling(sizes [3]int, prepare func(size int) (operation func())) Scaling {
	scaling := Scaling{Sizes: sizes}
	for i, size := range sizes {
		operation := prepare(size)
		best := Measure(operation)
		for range scalingRepeats - 1 {
			again := Measure(operation)
			best.Allocs, best.Bytes = min(best.Allocs, again.Allocs), min(best.Bytes, again.Bytes)
		}
		scaling.Cost[i] = best
	}
	return scaling
}

// Exponent is the power law that takes cost small at size smallN to cost large at size largeN: cost grows as size^Exponent. A constant cost has exponent 0, a linear cost 1, a quadratic cost 2. A cost of zero counts as one allocation or byte, so a path that starts at zero and grows reports a finite exponent.
func Exponent(smallN, largeN int, small, large float64) float64 {
	return math.Log((large+1)/(small+1)) / math.Log(float64(largeN)/float64(smallN))
}

// Exponents returns the largest exponent of the two decade steps (N to 10N, 10N to 100N) for allocations and bytes. The larger step is the one that shows a cost that is flat at small sizes and bends upward at large ones.
func (s Scaling) Exponents() (allocs, bytes float64) {
	allocs, bytes = math.Inf(-1), math.Inf(-1)
	for step := range 2 {
		allocs = max(allocs, Exponent(s.Sizes[step], s.Sizes[step+1], s.Cost[step].Allocs, s.Cost[step+1].Allocs))
		bytes = max(bytes, Exponent(s.Sizes[step], s.Sizes[step+1], s.Cost[step].Bytes, s.Cost[step+1].Bytes))
	}
	return allocs, bytes
}

// CheckScaling measures the operation at sizes N, 10N and 100N, logs every cost, and fails t when the cost grows faster than the ceilings of id+".allocs_exponent" and id+".bytes_exponent" allow. The ceiling names the exponent it pins: 0 for a cost independent of the size, 1 for a cost proportional to it, 2 for a quadratic one. A ceiling is the measured exponent rounded up with a margin for the constant part of the cost; it only moves down. It returns the measurement.
func CheckScaling(t testing.TB, id string, sizes [3]int, prepare func(size int) (operation func())) Scaling {
	t.Helper()
	if sizes[1] != 10*sizes[0] || sizes[2] != 10*sizes[1] {
		t.Fatalf("perfgate: %s: sizes %v are not N, 10N, 100N", id, sizes)
	}
	scaling := MeasureScaling(sizes, prepare)
	for i, size := range sizes {
		t.Logf("perfgate: %s at size %d: %.0f allocs %.0f B", id, size, scaling.Cost[i].Allocs, scaling.Cost[i].Bytes)
	}
	allocs, bytes := scaling.Exponents()
	Check(t, id+".allocs_exponent", round(allocs))
	Check(t, id+".bytes_exponent", round(bytes))
	return scaling
}

// round keeps two decimals, so a gate line reads the same on every run.
func round(value float64) float64 {
	return math.Round(value*100) / 100
}
