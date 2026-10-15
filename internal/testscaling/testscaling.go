// Package testscaling measures how the cost of a hot path grows with the size
// of the input that grows in a session (history length, message size, transcript
// lines, keys) and fails a test when the growth exceeds the declared bound.
//
// A gate runs the path at a size N and at 10N and compares the costs. The
// slope log10(cost(10N)/cost(N)) is about 0 for a constant path, 1 for a linear
// path and 2 for a quadratic path. Allocation count and allocated bytes are the
// gated costs because they do not depend on the load other processes put on the
// host; wall time is measured and reported only. Hardware instruction counters
// are not used: perf_event_open is refused on the shared hosts
// (kernel.perf_event_paranoid=4), and valgrind instruction counts belong to the
// Durable counts gate.
//
// A path that allocates nothing at either size has slopes of 0 and passes any bound: its CPU growth is reported in the wall-time slope but not gated, so a gate on such a path proves only that the path stays allocation-free.
//
// Only test code imports this package.
package testscaling

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"testing"
	"time"
)

// Order is the exponent of a cost bound in the growing size: 0 is constant,
// 1 is linear and 2 is quadratic. A logarithmic factor is within the tolerance
// of the next lower whole exponent.
type Order float64

// The bounds gates declare.
const (
	Constant  Order = 0
	Linear    Order = 1
	Quadratic Order = 2
)

func (order Order) String() string {
	switch order {
	case Constant:
		return "O(1)"
	case Linear:
		return "O(n)"
	case Quadratic:
		return "O(n^2)"
	}
	return fmt.Sprintf("O(n^%g)", float64(order))
}

// Tolerance is the slope a measurement may exceed its bound by. Over a tenfold
// size step it admits a factor of about 2.2 beyond the bound, which covers an
// n log n path declared linear and slice growth rounding, and it still fails a
// path that is one whole order worse than declared (a factor of 10).
const Tolerance = 0.35

// ReportEnv names a file each measured case appends one JSON line to, for
// building the complexity inventory.
const ReportEnv = "PIG_SCALING_REPORT"

// Cost is the cost of one run of a measured operation.
type Cost struct {
	Allocs float64
	Bytes  float64
	Nanos  float64
}

// Case is one gated hot path.
type Case struct {
	// Name identifies the path in failures and in the report.
	Name string
	// Size is N, the smaller of the two measured sizes; the larger is 10N.
	Size int
	// Bound is the declared growth of allocations and allocated bytes.
	Bound Order
	// BytesBound, when not zero, is the declared growth of allocated bytes where it differs from Bound: a path whose
	// allocation count is linear but that copies a slice of one 16-byte header per line on every frame allocates bytes
	// that grow a little faster than its size. Zero means Bound.
	BytesBound Order
	// Prepare builds the state for size n outside the measurement and returns
	// the operation to measure. Each call must return a fresh operation, since
	// an operation may consume its state.
	Prepare func(n int) func()
	// Runs is the number of fresh operations measured at each size; the least
	// cost of the runs is kept, so allocations of goroutines other tests left
	// running only add to a run and do not count. Zero means 3.
	Runs int
}

// Measure returns the least cost of runs fresh operations at size n.
func Measure(prepare func(n int) func(), n, runs int) Cost {
	if runs <= 0 {
		runs = 3
	}
	best := Cost{Allocs: math.Inf(1), Bytes: math.Inf(1), Nanos: math.Inf(1)}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	for range runs {
		op := prepare(n)
		// Two collections empty every sync.Pool (the first moves pooled objects to the victim cache, the second drops them), so each run starts cold
		// and its cost does not depend on what earlier runs or tests left pooled.
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		op()
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		best.Allocs = min(best.Allocs, float64(after.Mallocs-before.Mallocs))
		best.Bytes = min(best.Bytes, float64(after.TotalAlloc-before.TotalAlloc))
		best.Nanos = min(best.Nanos, float64(elapsed.Nanoseconds()))
	}
	return best
}

// Slope is log10(large/small) with a floor that keeps an operation that
// allocates nothing at one size from reading as infinite growth.
func Slope(small, large, floor float64) float64 {
	return math.Log10(max(large, floor) / max(small, floor))
}

// Result is the measurement of one case at N and 10N.
type Result struct {
	Name        string  `json:"name"`
	Bound       string  `json:"bound"`
	BytesBound  string  `json:"bytesBound,omitempty"`
	N           int     `json:"n"`
	Small       Cost    `json:"small"`
	Large       Cost    `json:"large"`
	AllocsSlope float64 `json:"allocsSlope"`
	BytesSlope  float64 `json:"bytesSlope"`
	TimeSlope   float64 `json:"timeSlope"`
}

// Floors under which a cost difference is noise rather than growth.
const (
	allocsFloor = 4
	bytesFloor  = 1024
	nanosFloor  = 10_000
)

// Run measures c at N and 10N.
func Run(c Case) Result {
	small := Measure(c.Prepare, c.Size, c.Runs)
	large := Measure(c.Prepare, 10*c.Size, c.Runs)
	result := Result{
		Name:        c.Name,
		Bound:       c.Bound.String(),
		N:           c.Size,
		Small:       small,
		Large:       large,
		AllocsSlope: Slope(small.Allocs, large.Allocs, allocsFloor),
		BytesSlope:  Slope(small.Bytes, large.Bytes, bytesFloor),
		TimeSlope:   Slope(small.Nanos, large.Nanos, nanosFloor),
	}
	if c.BytesBound != 0 {
		result.BytesBound = c.BytesBound.String()
	}
	return result
}

// Check measures c and fails t when the allocation or byte slope exceeds the
// declared bound by more than Tolerance.
func Check(t *testing.T, c Case) Result {
	t.Helper()
	if c.Size <= 0 || c.Prepare == nil || c.Name == "" {
		t.Fatalf("testscaling: case %q needs a name, a positive size and a prepare function", c.Name)
	}
	result := Run(c)
	t.Logf("%s: bound %s; n=%d allocs %.0f bytes %.0f time %s; n=%d allocs %.0f bytes %.0f time %s; slopes allocs %.2f bytes %.2f time %.2f",
		c.Name, result.Bound,
		c.Size, result.Small.Allocs, result.Small.Bytes, time.Duration(result.Small.Nanos),
		10*c.Size, result.Large.Allocs, result.Large.Bytes, time.Duration(result.Large.Nanos),
		result.AllocsSlope, result.BytesSlope, result.TimeSlope)
	bytesBound := c.Bound
	if c.BytesBound != 0 {
		bytesBound = c.BytesBound
	}
	for _, violation := range result.ViolationsWith(c.Bound, bytesBound) {
		t.Error(violation)
	}
	if path := os.Getenv(ReportEnv); path != "" {
		appendReport(t, path, result)
	}
	return result
}

// Violations describes each gated cost whose slope exceeds bound by more than Tolerance.
func (r Result) Violations(bound Order) []string { return r.ViolationsWith(bound, bound) }

// ViolationsWith is Violations with a separate bound for allocated bytes.
func (r Result) ViolationsWith(allocs, bytes Order) []string {
	var out []string
	for _, cost := range []struct {
		name  string
		slope float64
		bound Order
	}{{"allocations", r.AllocsSlope, allocs}, {"allocated bytes", r.BytesSlope, bytes}} {
		bound := cost.bound
		limit := float64(bound) + Tolerance
		if cost.slope > limit {
			out = append(out, fmt.Sprintf("%s: %s grew %.1fx from n=%d to n=%d (slope %.2f), bound %s allows slope %.2f",
				r.Name, cost.name, math.Pow(10, cost.slope), r.N, 10*r.N, cost.slope, bound, limit))
		}
	}
	return out
}

func appendReport(t *testing.T, path string, result Result) {
	t.Helper()
	line, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("testscaling: encode report: %v", err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("testscaling: open report: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("testscaling: close report: %v", err)
		}
	}()
	if _, err := file.Write(append(line, '\n')); err != nil {
		t.Fatalf("testscaling: write report: %v", err)
	}
}
