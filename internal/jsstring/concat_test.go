package jsstring

import "testing"

func TestJoinAdjacentSurrogates(t *testing.T) {
	high, low := FromUTF16([]uint16{0xd83d}), FromUTF16([]uint16{0xde00})
	for _, tc := range []struct{ in, want string }{
		{"", ""}, {"plain \u00e9\ud7ff", "plain \u00e9\ud7ff"}, {high, high}, {low, low}, {low + high, low + high}, {high + "x" + low, high + "x" + low},
		{high + low, "\U0001F600"}, {"a" + high + low + "b", "a\U0001F600b"}, {high + low + high + low, "\U0001F600\U0001F600"},
		{high + high + low + low, high + "\U0001F600" + low}, {"\ud7ff" + high + low + "\ud7ff", "\ud7ff\U0001F600\ud7ff"},
		{"\xed\xa0\xbd\xed\xb8", "\xed\xa0\xbd\xed\xb8"}, {"\xed\xa0\xbd\xed\xb8\x40", "\xed\xa0\xbd\xed\xb8\x40"},
	} {
		if got := JoinAdjacentSurrogates(tc.in); got != tc.want {
			t.Errorf("JoinAdjacentSurrogates(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if tc.in == tc.want {
			continue
		}
		if got := JoinAdjacentSurrogates(tc.in); got != Canonical(tc.in) {
			t.Errorf("JoinAdjacentSurrogates(%q) = %q, Canonical gives %q", tc.in, got, Canonical(tc.in))
		}
	}
}
