package sessions

import "testing"

// Pi cuts titles with `slice(0, n)`: UTF-16 units. A surrogate pair the limit falls inside is cut in two and SQLite stores
// the lone surrogate as U+FFFD.
func TestUTF16SliceCutsAtCodeUnits(t *testing.T) {
	for _, test := range []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abc", 2, "ab"},
		{"ab😀cd", 3, "ab\uFFFD"},
		{"ab😀cd", 4, "ab😀"},
		{"😀😀", 1, "\uFFFD"},
		{"é😀", 1, "é"},
		{"", 3, ""},
	} {
		if got := utf16Slice(test.in, test.n); got != test.want {
			t.Errorf("utf16Slice(%q, %d) = %q, want %q", test.in, test.n, got, test.want)
		}
	}
}
