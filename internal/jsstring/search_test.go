package jsstring

import (
	"slices"
	"testing"
)

func TestUTF16SearchPositions(t *testing.T) {
	high, low := FromUTF16([]uint16{0xd83d}), FromUTF16([]uint16{0xde00})
	for _, tc := range []struct {
		text, needle            string
		from, forward, backward int
	}{
		{"A😀B", high, 0, 1, -1}, {"A😀B", low, 4, -1, 2}, {"😀😀", low, 0, 1, -1},
		{"abc", "a", -1, 0, 0}, {"abc", "", 99, 3, 3}, {"abc", "abcd", 0, -1, -1},
		{"😀", "😀", 1, -1, 0},
	} {
		if got := IndexOf(tc.text, tc.needle, tc.from); got != tc.forward {
			t.Errorf("index(%q,%q,%d)=%d want=%d", tc.text, tc.needle, tc.from, got, tc.forward)
		}
		if got := LastIndexOf(tc.text, tc.needle, tc.from); got != tc.backward {
			t.Errorf("lastIndex(%q,%q,%d)=%d want=%d", tc.text, tc.needle, tc.from, got, tc.backward)
		}
	}
}

// IndexOf's ASCII path (a byte index is the UTF-16 index) answers as the UTF-16 search does, for every start and needle of mixed texts.
func TestIndexOfASCIIPathMatchesUTF16Search(t *testing.T) {
	utf16Index := func(text, search string, from int) int {
		units, needle := ToUTF16(text), ToUTF16(search)
		from = max(0, min(from, len(units)))
		for pos := from; pos+len(needle) <= len(units); pos++ {
			if slices.Equal(units[pos:pos+len(needle)], needle) {
				return pos
			}
		}
		return -1
	}
	texts := []string{"", "/", "openai/gpt-4o", "a/b/c", "//", "openai/gpt-4o:high", "no slash", "é/x", "ab😀/c"}
	needles := []string{"", "/", "//", "gpt", "x", "é", "😀", "zz", "c"}
	for _, text := range texts {
		for _, needle := range needles {
			for from := -2; from <= len(text)+2; from++ {
				if got, want := IndexOf(text, needle, from), utf16Index(text, needle, from); got != want {
					t.Errorf("IndexOf(%q, %q, %d) = %d, want %d", text, needle, from, got, want)
				}
			}
		}
	}
}
