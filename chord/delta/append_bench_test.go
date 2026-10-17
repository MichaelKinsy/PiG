package delta

import (
	"fmt"
	"reflect"
	"testing"
)

// The cost of an append that Pi 1.2.0 fixed (chord CHANGELOG 1.2.0 Fixed: an append no longer walks the whole array;
// applyImmutable builds an appended array in one copy). Run with -benchmem.

func benchValues(size int) []any {
	values := make([]any, size)
	for index := range values {
		values[index] = float64(index)
	}
	return values
}

// BenchmarkApplyImmutableAppend replays one appended item onto an array the replay has not copied yet.
func BenchmarkApplyImmutableAppend(b *testing.B) {
	for _, size := range []int{10, 1_000, 100_000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			base := JsonObjectOf("values", benchValues(size))
			ops := []Op{{"p", []any{"values"}, size, 0, []any{-1.0}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ApplyImmutable(base, ops); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTrackerAppend prepares a change that appends one item to a tracked array.
func BenchmarkTrackerAppend(b *testing.B) {
	for _, size := range []int{10, 1_000, 100_000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			tracker := Track(JsonObjectOf("values", benchValues(size)))
			b.ReportAllocs()
			for b.Loop() {
				change := tracker.BeginChange()
				if _, err := change.State().Array("values").Push(-1.0); err != nil {
					b.Fatal(err)
				}
				prepared, err := change.Prepare()
				if err != nil {
					b.Fatal(err)
				}
				prepared.Abort()
			}
		})
	}
}

// BenchmarkTrackerOrderedSplice prepares a change that inserts or removes one element at the start, middle or end of a
// tracked array: an order-preserving change, which Pi 1.2.0 tracker.ts orderedArrayPlan plans from its pieces alone.
func BenchmarkTrackerOrderedSplice(b *testing.B) {
	for _, size := range []int{1_000, 100_000} {
		for _, at := range []struct {
			name  string
			index int
		}{{"start", 0}, {"middle", size / 2}, {"end", size}} {
			for _, edit := range []struct {
				name   string
				remove int
				items  []any
			}{{"insert", 0, []any{-1.0}}, {"remove", 1, nil}} {
				index := at.index
				if edit.remove > 0 && index == size {
					index = size - 1
				}
				b.Run(fmt.Sprintf("%s/%s/%d", edit.name, at.name, size), func(b *testing.B) {
					tracker := Track(JsonObjectOf("values", benchValues(size)))
					b.ReportAllocs()
					for b.Loop() {
						change := tracker.BeginChange()
						if _, err := change.State().Array("values").Splice(index, edit.remove, edit.items...); err != nil {
							b.Fatal(err)
						}
						prepared, err := change.Prepare()
						if err != nil {
							b.Fatal(err)
						}
						prepared.Abort()
					}
				})
			}
		}
	}
}

// delta/index.ts:429-473: an append to an array not yet copied in the replay is one copy of the array plus the items,
// not a copy the insert then grows.
func TestApplyImmutableAppendCopiesTheArrayOnce(t *testing.T) {
	const size = 100_000
	base := JsonObjectOf("values", benchValues(size))
	ops := []Op{{"p", []any{"values"}, size, 0, []any{-1.0}}}
	oneCopy := float64(size) * 16 // one []any of size elements
	allocated := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := ApplyImmutable(base, ops); err != nil {
				b.Fatal(err)
			}
		}
	}).AllocedBytesPerOp()
	if float64(allocated) > 1.5*oneCopy {
		t.Fatalf("an append allocated %d bytes, more than 1.5 copies of the %d-element array (%.0f bytes)", allocated, size, oneCopy)
	}
	appended, err := ApplyImmutable(base, ops)
	if err != nil {
		t.Fatal(err)
	}
	values := appended.(*JsonObject).Value("values").([]any)
	if len(values) != size+1 || values[size] != -1.0 || len(base.Value("values").([]any)) != size {
		t.Fatalf("appended length %d, last %v; base length %d", len(values), values[len(values)-1], len(base.Value("values").([]any)))
	}
}

// tracker.ts orderedArrayPlan (Pi 1.2.0): appending to a long array plans one insert from the changed range alone, with
// no per-element work over the array; only the prepared value's copy of the array grows with its length.
func TestTrackerAppendDoesNotWalkTheArray(t *testing.T) {
	const size = 100_000
	tracker := Track(JsonObjectOf("values", benchValues(size)))
	allocs := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			change := tracker.BeginChange()
			if _, err := change.State().Array("values").Push(-1.0); err != nil {
				b.Fatal(err)
			}
			prepared, err := change.Prepare()
			if err != nil {
				b.Fatal(err)
			}
			prepared.Abort()
		}
	}).AllocsPerOp()
	if allocs > 200 {
		t.Fatalf("an append to a %d-element array made %d allocations per change", size, allocs)
	}
}

// tracker.ts orderedArrayPlan over the overlay's pieces (Pi 1.2.0): an insert or removal anywhere in a long array that
// keeps the remaining elements in order splits and joins pieces and is planned from them, with no per-element work.
func TestTrackerOrderedSpliceDoesNotWalkTheArray(t *testing.T) {
	const size = 100_000
	tracker := Track(JsonObjectOf("values", benchValues(size)))
	for _, edit := range []struct {
		name          string
		index, remove int
		items         []any
		ops           []Op
	}{
		{"insert at the start", 0, 0, []any{-1.0}, []Op{{"p", []any{"values"}, 0, 0, []any{-1.0}}}},
		{"insert in the middle", size / 2, 0, []any{-1.0}, []Op{{"p", []any{"values"}, size / 2, 0, []any{-1.0}}}},
		{"remove at the start", 0, 1, nil, []Op{{"p", []any{"values"}, 0, 1, []any{}}}},
		{"remove in the middle", size / 2, 1, nil, []Op{{"p", []any{"values"}, size / 2, 1, []any{}}}},
		{"remove at the end", size - 1, 1, nil, []Op{{"p", []any{"values"}, size - 1, 1, []any{}}}},
	} {
		t.Run(edit.name, func(t *testing.T) {
			prepare := func() *Prepared {
				change := tracker.BeginChange()
				if _, err := change.State().Array("values").Splice(edit.index, edit.remove, edit.items...); err != nil {
					t.Fatal(err)
				}
				prepared, err := change.Prepare()
				if err != nil {
					t.Fatal(err)
				}
				return prepared
			}
			prepared := prepare()
			if !reflect.DeepEqual(prepared.Ops(), edit.ops) {
				t.Fatalf("ops %v, want %v", prepared.Ops(), edit.ops)
			}
			prepared.Abort()
			allocs := testing.Benchmark(func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					prepare().Abort()
				}
			}).AllocsPerOp()
			if allocs > 200 {
				t.Fatalf("%s of a %d-element array made %d allocations per change", edit.name, size, allocs)
			}
		})
	}
}
