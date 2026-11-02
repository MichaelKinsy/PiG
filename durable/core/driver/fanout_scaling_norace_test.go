//go:build !race

package driver_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// The race detector inflates allocations, so the measurement runs without it.
// A tool that fans out calls nested calls costs a number of allocations and bytes linear in the calls: admitting one
// nested call does not ship, decode or walk what the earlier calls left in the live document.
func TestScalingCoreNestedCallFanout(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "core nested call fan-out", Size: 100, Bound: testscaling.Linear, Prepare: func(calls int) func() {
		return coreFanout(t, calls)
	}})
}
