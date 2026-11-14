package json

import (
	"slices"
	"sort"
	"strconv"
	"testing"
)

// The two sorts of typeFields run through sort.Slice. It leaves fields in the places slices.SortFunc left them, equal
// keys included: both are the same pattern-defeating quicksort. nameNonEsc tells fields with one key apart here.
func TestFieldSortsPlaceEqualKeysAsSlicesSortFunc(t *testing.T) {
	marks := func(fields []field) []string {
		out := make([]string, len(fields))
		for i, f := range fields {
			out[i] = f.nameNonEsc
		}
		return out
	}
	for _, n := range []int{0, 1, 2, 12, 13, 64, 257} {
		fields := make([]field, n)
		for i := range fields {
			// Five names, two tag values and three index sequences: the keys repeat from the 31st field on.
			fields[i] = field{name: string(rune('a' + i*7%5)), tag: i%2 == 0, index: []int{i % 3}, nameNonEsc: strconv.Itoa(i)}
		}
		got, want := slices.Clone(fields), slices.Clone(fields)
		sort.Slice(got, func(i, j int) bool { return compareFieldsByName(&got[i], &got[j]) < 0 })
		slices.SortFunc(want, func(a, b field) int { return compareFieldsByName(&a, &b) })
		if !slices.Equal(marks(got), marks(want)) {
			t.Errorf("%d fields by name: sort.Slice %v, slices.SortFunc %v", n, marks(got), marks(want))
		}
		got, want = slices.Clone(fields), slices.Clone(fields)
		sort.Slice(got, func(i, j int) bool { return slices.Compare(got[i].index, got[j].index) < 0 })
		slices.SortFunc(want, func(a, b field) int { return slices.Compare(a.index, b.index) })
		if !slices.Equal(marks(got), marks(want)) {
			t.Errorf("%d fields by index: sort.Slice %v, slices.SortFunc %v", n, marks(got), marks(want))
		}
	}
}
