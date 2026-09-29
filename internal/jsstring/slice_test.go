package jsstring

import (
	"slices"
	"testing"
)

func TestUTF16SlicingAndSplicing(t *testing.T) {
	text := "A😀B"
	if Length(text) != 4 {
		t.Fatal("string length must count UTF-16 units")
	}
	for _, tc := range []struct {
		start, end int
		want       []uint16
	}{{1, 2, []uint16{0xd83d}}, {2, 3, []uint16{0xde00}}, {-3, -1, []uint16{0xd83d, 0xde00}}, {20, 30, []uint16{}}, {3, 1, []uint16{}}} {
		if got := ToUTF16(Slice(text, tc.start, tc.end)); !slices.Equal(got, tc.want) {
			t.Errorf("slice(%d,%d)=%x want=%x", tc.start, tc.end, got, tc.want)
		}
	}
	if got := ToUTF16(Splice(text, 2, 2, "X")); !slices.Equal(got, []uint16{'A', 0xd83d, 'X', 0xde00, 'B'}) {
		t.Fatalf("splice=%x", got)
	}
	if got := Splice("abc", -1, 3, "X"); got != "abX" {
		t.Fatal(got)
	}
	high, low := FromUTF16([]uint16{0xd83d}), FromUTF16([]uint16{0xde00})
	if r, n := DecodeRuneInString(high); r != 0xd83d || n != 3 {
		t.Fatalf("lone decode=%x,%d", r, n)
	}
	if r, n := DecodeRuneInString(high + low); r != 0x1f600 || n != 6 {
		t.Fatalf("joined decode=%x,%d", r, n)
	}
	if Splice(high, 1, 1, low) != "😀" {
		t.Fatal("joining units did not preserve the scalar string")
	}
}
