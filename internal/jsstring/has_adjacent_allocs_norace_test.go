//go:build !race

package jsstring

import "testing"

func TestHasAdjacentSurrogatesDoesNotAllocate(t *testing.T) {
	high := FromUTF16([]uint16{0xd83d})
	for _, text := range []string{"plain text of some length", "caf\u00e9 \ud7ff \U0001F600", "a" + high + "b", "a" + high + "\xed\xb8"} {
		if n := testing.AllocsPerRun(100, func() { _ = HasAdjacentSurrogates(text) }); n != 0 {
			t.Errorf("HasAdjacentSurrogates(%q) allocates %v times", text, n)
		}
	}
}
