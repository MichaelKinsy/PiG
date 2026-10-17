package truncate

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// truncate.ts truncateTail reads only the end of its input: the last maxLines lines, at most maxBytes bytes, and the totals. TailWindow keeps a bounded
// suffix and the totals, so for any sequence of chunks its Result must equal TruncateTail of the concatenation, field by field.
//
// mutation-checked: a window of maxBytes+1 bytes, a missing trailing-newline correction, and counting an empty content as a line each fail it.
func TestTailWindowMatchesTruncateTailOnEveryChunkSequence(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	pieces := []string{"a", "bc", "\n", "\n\n", "é", "日本", "line of text\n", strings.Repeat("x", 7), strings.Repeat("y\n", 5), strings.Repeat("z", 40)}
	for _, limits := range []struct{ maxBytes, maxLines int }{{16, 3}, {17, 4}, {40, 50}, {8, 2}, {64, 5}, {DefaultMaxBytes, DefaultMaxLines}} {
		for run := range 400 {
			window := NewTailWindow(limits.maxBytes, limits.maxLines)
			var whole strings.Builder
			for step := range 1 + rng.IntN(30) {
				var chunk strings.Builder
				for range rng.IntN(6) {
					chunk.WriteString(pieces[rng.IntN(len(pieces))])
				}
				if rng.IntN(25) == 0 {
					chunk.WriteString(strings.Repeat("é", 1+rng.IntN(2*limits.maxBytes)))
				}
				window.Append(chunk.String())
				whole.WriteString(chunk.String())
				if got, want := window.Result(), TruncateTail(whole.String(), limits.maxBytes, limits.maxLines); !reflect.DeepEqual(got, want) {
					t.Fatalf("limits %+v run %d step %d: window result\n  %+v\nwant TruncateTail\n  %+v", limits, run, step, got, want)
				}
			}
			reset := strings.Repeat("reset\n", 1+rng.IntN(40))
			window.Reset(reset)
			if got, want := window.Result(), TruncateTail(reset, limits.maxBytes, limits.maxLines); !reflect.DeepEqual(got, want) {
				t.Fatalf("limits %+v run %d: after Reset %+v, want %+v", limits, run, got, want)
			}
		}
	}
}

// A window never reads more than maxBytes+2 bytes or holds more than twice that however much is appended, which is what makes the per-frame cost
// independent of the output size.
func TestTailWindowKeepsABoundedSuffix(t *testing.T) {
	window := NewTailWindow(100, 10)
	for range 1000 {
		window.Append(strings.Repeat("0123456789", 50))
	}
	if len(window.window()) != 102 || len(window.buf) > 204 || window.Result().TotalBytes != 500_000 {
		t.Fatalf("window %d bytes of %d held, total %d; want 102 of at most 204, and 500000", len(window.window()), len(window.buf), window.Result().TotalBytes)
	}
	small := NewTailWindow(100, 10)
	for range 1000 {
		small.Append("0123456")
	}
	if len(small.window()) != 102 || len(small.buf) > 204 || small.Result().TotalBytes != 7_000 {
		t.Fatalf("small chunks: window %d bytes of %d held, total %d; want 102 of at most 204, and 7000", len(small.window()), len(small.buf), small.Result().TotalBytes)
	}
}
