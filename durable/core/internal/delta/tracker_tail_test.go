package delta

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func numberArray(n int) []any {
	values := make([]any, n)
	for index := range values {
		values[index] = float64(index)
	}
	return values
}

// appendOne is the operation of a nested call's admission: one element appended to a long array, prepared and not adopted.
func appendOne(tb testing.TB, tracker *Tracker) {
	tb.Helper()
	change := tracker.BeginChange()
	if _, err := change.State().Arr("values").Push(-1.0); err != nil {
		tb.Fatal(err)
	}
	prepared, err := change.Prepare()
	if err != nil {
		tb.Fatal(err)
	}
	prepared.Abort()
}

// A change confined to the tail of an array (elements appended, a suffix removed, an element overwritten, an element
// read as an object and edited) emits the operations and the value the element-per-entry list does.
func TestTailOverlayMatchesTheEntryList(t *testing.T) {
	for seed := range uint64(400) {
		random := rand.New(rand.NewPCG(seed, 7))
		length := random.IntN(12)
		build := func() *Tracker {
			values := make([]any, length)
			for index := range values {
				if index%3 == 0 {
					item := jv.NewObject()
					item.Set("n", float64(index))
					values[index] = item
				} else {
					values[index] = float64(index)
				}
			}
			root := jv.NewObject()
			root.Set("values", values)
			return Track(root)
		}
		tail, entries := build().BeginChange(), build().BeginChange()
		arrays := [2]*Arr{tail.State().Arr("values"), entries.State().Arr("values")}
		arrays[1].n.ensureEntries()
		for step := range 1 + random.IntN(8) {
			size := arrays[0].Len()
			choice := random.IntN(6)
			index := random.IntN(size + 1)
			for which, array := range arrays {
				var err error
				switch choice {
				case 0:
					_, err = array.Push(float64(step), "x")
				case 1:
					array.Pop()
				case 2:
					err = array.Set(index, float64(100+step))
				case 3:
					if object := array.Obj(index); object != nil {
						err = object.Set("edited", float64(step))
					}
				case 4:
					_, err = array.Splice(index, size-index, "tail", float64(step))
				case 5:
					array.SetLen(index)
				}
				if err != nil {
					t.Fatalf("seed %d step %d (%d) variant %d: %v", seed, step, choice, which, err)
				}
			}
		}
		preparedTail, errTail := tail.Prepare()
		preparedEntries, errEntries := entries.Prepare()
		if errTail != nil || errEntries != nil {
			t.Fatalf("seed %d: prepare: %v, %v", seed, errTail, errEntries)
		}
		if got, want := jv.Stringify(OpsValue(preparedTail.Ops())), jv.Stringify(OpsValue(preparedEntries.Ops())); got != want {
			t.Fatalf("seed %d: operations %s, the entry list's %s", seed, got, want)
		}
		if got, want := jv.Stringify(preparedTail.Value()), jv.Stringify(preparedEntries.Value()); got != want {
			t.Fatalf("seed %d: value %s, the entry list's %s", seed, got, want)
		}
	}
}

// core copy of the tracker: one-item append to an n-element array: Track -> Push -> Prepare (emitOperations + Apply).
func BenchmarkTrackerAppend(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			root := jv.NewObject()
			root.Set("values", numberArray(n))
			tracker := Track(root)
			b.ReportAllocs()
			for b.Loop() {
				appendOne(b, tracker)
			}
		})
	}
}

// core Apply of one appended item.
func BenchmarkApplyAppend(b *testing.B) {
	for _, n := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			root := jv.NewObject()
			root.Set("values", numberArray(n))
			ops := []Op{{Verb: Splice, Path: Path{Key("values")}, Index: n, Remove: 0, Items: []any{-1.0}}}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := Apply(root, ops); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
