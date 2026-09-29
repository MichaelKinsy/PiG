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
