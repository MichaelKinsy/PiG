package json

import (
	"slices"
	"strconv"
	"testing"
)

// tiedKeyFields returns n fields whose keys tie on a prefix: five names, two tag values, index sequences of one and of two numbers, all distinct. Every third field is followed by a byte for byte copy of itself, as typeFields appends for a field reached twice at one depth. nameNonEsc numbers the fields, so fields that the sort puts in other places can be told apart (a copy carries the number of its field).
func tiedKeyFields(n int) []field {
	fields := make([]field, 0, n+n/3+1)
	for i := range n {
		index := []int{i}
		if i%3 != 0 {
			index = []int{i % 3, i}
		}
		fields = append(fields, field{name: string(rune('a' + i*7%5)), tag: i%2 == 0, index: index, nameNonEsc: strconv.Itoa(i)})
		if i%3 == 1 {
			fields = append(fields, fields[len(fields)-1])
		}
	}
	return fields
}

func fieldMarks(fields []field) []string {
	marks := make([]string, len(fields))
	for i, f := range fields {
		marks[i] = f.nameNonEsc
	}
	return marks
}

// sortFields puts fields where slices.SortFunc puts them, in both orders typeFields sorts by, from the order typeFields builds the list in and from its reverse, with keys that tie on a prefix (name; name and depth; name, depth and tag) and with the copies typeFields appends.
func TestSortFieldsPlacesFieldsAsSlicesSortFunc(t *testing.T) {
	for _, order := range []struct {
		name string
		cmp  func(a, b *field) int
	}{{"name", compareFieldsByName}, {"index", compareFieldsByIndex}} {
		for _, n := range []int{0, 1, 2, 3, 12, 13, 64, 257} {
			for _, reversed := range []bool{false, true} {
				fields := tiedKeyFields(n)
				if reversed {
					slices.Reverse(fields)
				}
				got, want := slices.Clone(fields), slices.Clone(fields)
				sortFields(got, order.cmp)
				slices.SortFunc(want, func(a, b field) int { return order.cmp(&a, &b) })
				if !slices.Equal(fieldMarks(got), fieldMarks(want)) {
					t.Errorf("%d fields by %s (reversed %v): sortFields %v, slices.SortFunc %v", n, order.name, reversed, fieldMarks(got), fieldMarks(want))
				}
			}
		}
	}
}

// A sort allocates nothing. sort.Slice allocated three objects a call, six for the first use of each struct type.
func TestSortFieldsAllocatesNothing(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector allocates")
	}
	fields := tiedKeyFields(40)
	if got := testing.AllocsPerRun(50, func() {
		sortFields(fields, compareFieldsByName)
		sortFields(fields, compareFieldsByIndex)
	}); got != 0 {
		t.Errorf("sortFields allocated %v objects a run, want none", got)
	}
}

type fieldTieTagged struct {
	A int `json:"N"`
}

type fieldTiePlainB struct{ N string }

// Two untagged fields of one name and depth tie on name and depth and differ in index only: neither dominates, both go.
type fieldTiePlain struct {
	fieldOrderPlain
	fieldTiePlainB
	Z int
}

// The two untagged fields tie with each other, and the tagged field of their name and depth wins over both.
type fieldTiePlainAndTagged struct {
	fieldOrderPlain
	fieldTiePlainB
	fieldTieTagged
	Z int
}

// The ties between fields of one name resolve as encoding/json resolves them, and the survivors come out in declaration order.
func TestStructMemberOrderWithTiedKeys(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		want  string
	}{
		{"untagged fields tie on name and depth", fieldTiePlain{Z: 3}, `{"Z":3}`},
		{"a tagged field wins over two untagged ones that tie", fieldTiePlainAndTagged{fieldTieTagged: fieldTieTagged{A: 5}, Z: 3}, `{"N":5,"Z":3}`},
	} {
		got, err := Marshal(test.value)
		if err != nil {
			t.Errorf("%s: %v", test.name, err)
			continue
		}
		if string(got) != test.want {
			t.Errorf("%s: Marshal = %s, want %s", test.name, got, test.want)
		}
	}
}
