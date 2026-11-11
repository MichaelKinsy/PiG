//go:build !race

package delta

import (
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// An element appended to an array, and an element read or overwritten at its end, allocate the same however long the array
// is, except for the one copy of the array that materializes the candidate revision (bytes, never allocations): the draft
// keeps nothing for the elements no one touched.
func TestScalingTrackerAppendToALongArray(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "core tracker, append to an array", Size: 1000, Bound: testscaling.Constant, BytesBound: testscaling.Linear, Prepare: func(n int) func() {
		root := jv.NewObject()
		root.Set("values", numberArray(n))
		tracker := Track(root)
		return func() { appendOne(t, tracker) }
	}})
}

func TestScalingTrackerReadsOfALongArray(t *testing.T) {
	testscaling.Check(t, testscaling.Case{Name: "core tracker, length and last element of an array", Size: 1000, Bound: testscaling.Constant, Prepare: func(n int) func() {
		slots := make([]any, n)
		for index := range slots {
			slot := jv.NewObject()
			slot.Set("taskId", float64(index))
			slots[index] = slot
		}
		root := jv.NewObject()
		root.Set("slots", slots)
		change := Track(root).BeginChange()
		return func() {
			array := change.State().Arr("slots")
			for range 100 {
				if array.Len() != n || array.Obj(n-1).Get("taskId") != float64(n-1) {
					t.Fatal("wrong element")
				}
			}
		}
	}})
}
