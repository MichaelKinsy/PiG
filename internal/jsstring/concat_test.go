package jsstring

import "testing"

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
