package jsstring

import (
	"strings"
	"testing"
)

// Pi main 9e870f6f0 packages/coding-agent/test/truncate-surrogates.test.ts
func TestSliceHeadAndTailNeverSplitAPair(t *testing.T) {
	const emoji = "\U0001F600"
	text := emoji + "x" + emoji
	loneFree := func(s string) bool { return !strings.Contains(s, "\xed") }
	for units := 0; units <= 6; units++ {
		head, tail := SliceHead(text, units), SliceTail(text, units)
		if !loneFree(head) || !loneFree(tail) {
			t.Fatalf("units=%d head=%q tail=%q holds a lone surrogate", units, head, tail)
		}
		if !strings.HasPrefix(text, head) || !strings.HasSuffix(text, tail) {
			t.Fatalf("units=%d head=%q tail=%q", units, head, tail)
		}
		want := min(units, 5) - 1
		if Length(head) < want || Length(tail) < want {
			t.Fatalf("units=%d dropped more than one unit: %q %q", units, head, tail)
		}
		hu, tu := FromUTF16(HeadUnits(ToUTF16(text), units)), FromUTF16(TailUnits(ToUTF16(text), units))
		if hu != head || tu != tail {
			t.Fatalf("units=%d unit helpers differ: %q %q", units, hu, tu)
		}
	}
}

func TestSliceHeadAndTailMatchSliceWithoutSurrogates(t *testing.T) {
	for _, c := range []struct {
		got, want string
	}{
		{SliceHead("abcdef", 3), "abc"}, {SliceTail("abcdef", 3), "def"},
		{SliceHead("abc", 10), "abc"}, {SliceTail("abc", 10), "abc"},
		{SliceHead("abc", 0), ""}, {SliceTail("abc", 0), ""},
	} {
		if c.got != c.want {
			t.Fatalf("got %q want %q", c.got, c.want)
		}
	}
}
