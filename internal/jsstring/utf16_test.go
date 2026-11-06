package jsstring

import (
	"bytes"
	"slices"
	"testing"
	"unicode/utf16"
)

func TestUTF16RoundTripPreservesLoneSurrogates(t *testing.T) {
	for _, units := range [][]uint16{{}, {0}, {'a', 0, 'b'}, {0xd83d, 0xde00}, {0xd83d, 'X', 0xde00}, {0xd800, 0xd800}, {0xdc00}, {0xffff}} {
		if got := ToUTF16(FromUTF16(units)); !slices.Equal(got, units) {
			t.Fatalf("units=%x round trip=%x", units, got)
		}
	}
	if got := FromUTF16(utf16.Encode([]rune("café 日本語 😀"))); got != "café 日本語 😀" {
		t.Fatal(got)
	}
	if got := ToUTF8(FromUTF16([]uint16{0xd800, 0xd800})); !bytes.Equal(got, []byte("��")) {
		t.Fatalf("UTF8=%x", got)
	}
}

func TestCanonicalJoinsAdjacentSurrogateHalves(t *testing.T) {
	high, low := FromUTF16([]uint16{0xd83d}), FromUTF16([]uint16{0xde00})
	for _, tc := range []struct{ in, want string }{
		{"", ""}, {"plain \u00e9\ud7ff", "plain \u00e9\ud7ff"}, {high, high}, {low, low}, {low + high, low + high}, {high + "x" + low, high + "x" + low},
		{high + low, "\U0001F600"}, {"a" + high + low + "b", "a\U0001F600b"}, {high + low + high + low, "\U0001F600\U0001F600"},
		{high + high + low + low, high + "\U0001F600" + low}, {"\ud7ff" + high + low + "\ud7ff", "\ud7ff\U0001F600\ud7ff"},
	} {
		if got := Canonical(tc.in); got != tc.want {
			t.Errorf("Canonical(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHasAdjacentSurrogates(t *testing.T) {
	high, low := FromUTF16([]uint16{0xd83d}), FromUTF16([]uint16{0xde00})
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"", false}, {"plain \u00e9\ud7ff", false}, {high, false}, {low, false}, {low + high, false}, {high + "x" + low, false}, {high + high, false},
		{high + low, true}, {"a" + high + low + "b", true}, {"\ud7ff\ud7ff" + high + low, true}, {high + high + low, true}, {"\xed\xa0\xbd\xed\xb8", false},
		{"\xed\xa0\xbd\xed\xb8\x40", false}, {"\xed\xa0\xbd\xed", false}, {"abc" + high + "\xed\xb8", false},
	} {
		if got := HasAdjacentSurrogates(tc.in); got != tc.want {
			t.Errorf("HasAdjacentSurrogates(%q) = %v, want %v", tc.in, got, tc.want)
		}
		if tc.want && Canonical(tc.in) == tc.in {
			t.Errorf("Canonical(%q) joined nothing", tc.in)
		}
	}
}
