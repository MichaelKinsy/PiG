//go:build !race

package perfgate

import (
	"math"
	"testing"
)

var sink []*int

// allocateEach is an operation of size allocations: linear in the size.
func allocateEach(size int) func() {
	return func() {
		sink = sink[:0]
		for range size {
			sink = append(sink, new(int))
		}
	}
}

// allocateEachOfEach is an operation of size*size/100 allocations: quadratic in the size.
func allocateEachOfEach(size int) func() {
	return func() {
		sink = sink[:0]
		for range size * size / 100 {
			sink = append(sink, new(int))
		}
	}
}

func allocateOnce(int) func() {
	return func() { sink = []*int{new(int)} }
}

func TestExponentOfKnownPowerLaws(t *testing.T) {
	for _, test := range []struct {
		name         string
		small, large float64
		want         float64
	}{
		{"constant", 5, 5, 0},
		{"linear", 1000, 10000, 1},
		{"quadratic", 1000, 100000, 2},
		{"square root", 1000, 3162, 0.5},
	} {
		got := Exponent(100, 1000, test.small, test.large)
		if math.Abs(got-test.want) > 0.01 {
			t.Errorf("%s: exponent %.3f, want %.2f", test.name, got, test.want)
		}
	}
	if got := Exponent(10, 100, 0, 0); got != 0 {
		t.Errorf("a cost of zero at both sizes has exponent %v, want 0", got)
	}
	if got := Exponent(10, 100, 0, 99); got != 2 {
		t.Errorf("a cost that starts at zero and reaches 99 reports exponent %v, want 2", got)
	}
}

func TestMeasureScalingSeesLinearQuadraticAndConstantOperations(t *testing.T) {
	sizes := [3]int{100, 1000, 10000}
	for _, test := range []struct {
		name    string
		prepare func(int) func()
		want    float64
	}{
		{"constant", allocateOnce, 0},
		{"linear", allocateEach, 1},
		{"quadratic", allocateEachOfEach, 2},
	} {
		allocs, _ := MeasureScaling(sizes, test.prepare).Exponents()
		if math.Abs(allocs-test.want) > 0.1 {
			t.Errorf("%s: allocation exponent %.2f, want %.1f", test.name, allocs, test.want)
		}
	}
}

func TestCheckScalingRejectsSizesThatAreNotADecadeApart(t *testing.T) {
	probe := &recorder{TB: t}
	func() {
		defer func() { _ = recover() }()
		CheckScaling(probe, "x.y", [3]int{100, 500, 1000}, allocateOnce)
	}()
	if !probe.fatal {
		t.Fatal("CheckScaling accepted sizes that are not N, 10N, 100N")
	}
}

type recorder struct {
	testing.TB
	fatal bool
}

func (r *recorder) Helper() {}
func (r *recorder) Fatalf(string, ...any) {
	r.fatal = true
	panic("fatal")
}
