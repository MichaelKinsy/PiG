package jsstring

import "testing"

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
