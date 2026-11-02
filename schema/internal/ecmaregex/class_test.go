package ecmaregex

import (
	"slices"
	"testing"
)

func TestRuneSetOperations(t *testing.T) {
	a := newRuneSet([]runeRange{{'d', 'f'}, {'a', 'c'}, {'x', 'x'}, {'e', 'h'}})
	if want := (runeSet{{'a', 'h'}, {'x', 'x'}}); !slices.Equal(a, want) {
		t.Fatalf("newRuneSet merged to %v, want %v", a, want)
	}
	b := runeSet{{'c', 'e'}, {'w', 'z'}}
	cases := []struct {
		name string
		got  runeSet
		want runeSet
	}{
		{"union", a.union(b), runeSet{{'a', 'h'}, {'w', 'z'}}},
		{"minus", a.minus(b), runeSet{{'a', 'b'}, {'f', 'h'}}},
		{"minus nothing", a.minus(), a},
		{"minus everything", a.minus(runeSet{{0, maxCodePoint}}), runeSet{}},
		{"complement", runeSet{{'b', 'c'}, {'e', 'e'}}.complement('f'), runeSet{{0, 'a'}, {'d', 'd'}, {'f', 'f'}}},
		{"complement of empty", runeSet(nil).complement(maxUnit), runeSet{{0, maxUnit}}},
		{"complement of all", runeSet{{0, maxUnit}}.complement(maxUnit), nil},
		{"complement cuts at the limit", runeSet{{'a', 0x2000}}.complement(0x1000), runeSet{{0, '`'}}},
		{"parseRangeList", parseRangeList("41-5a,61,7a-7b"), runeSet{{'A', 'Z'}, {'a', 'a'}, {'z', '{'}}},
	}
	for _, c := range cases {
		if !slices.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	for _, r := range []rune{'a', 'h', 'x'} {
		if !a.contains(r) {
			t.Errorf("contains(%q) = false", r)
		}
	}
	for _, r := range []rune{'`', 'i', 'w', 'y', 0} {
		if a.contains(r) {
			t.Errorf("contains(%q) = true", r)
		}
	}
}
