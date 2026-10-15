package tui

import (
	"math"
	"strings"
	"testing"
	"unicode/utf8"
)

// Terminal bytes in any two-chunk split never panic the stdin buffer or the key decoders, and the buffer's output for valid UTF-8 input is never longer than the input plus re-added bracketed-paste markers.
func FuzzStdinBufferChunksAreLossless(f *testing.F) {
	for _, seed := range []string{"abc", "\x1b[A\x1b[B", "\x1b[200~paste\x1b[201~x", "\x1b[27;5;13~", "\x1b[1;5C", "\x1b", "\x1b\x1b", "é日😀", "\xff\xfe", "\x1b[<0;10;5M", "\x1b[?62;4c", "\x1bP>|x\x1b\\", "\x1b]11;rgb:0000/0000/0000\x07", "\x1b_Gi=1;OK\x1b\\", "\x1b[97;1:3u", "\r\n"} {
		f.Add([]byte(seed), 2)
	}
	f.Add([]byte("ab"), math.MinInt)
	f.Fuzz(func(t *testing.T, input []byte, cut int) {
		// A negative cut (math.MinInt included, whose negation overflows) maps into [0, len(input)].
		cut %= len(input) + 1
		if cut < 0 {
			cut += len(input) + 1
		}
		buffer := NewStdinBuffer(StdinBufferOptions{})
		var out []string
		out = append(out, buffer.ProcessBytes(input[:cut])...)
		out = append(out, buffer.ProcessBytes(input[cut:])...)
		out = append(out, buffer.Flush()...)
		for _, chunk := range out {
			_ = IsKeyRelease(chunk)
			_ = IsKeyRepeat(chunk)
			_, _ = DecodeKittyPrintable(chunk)
			_, _ = ParseKey(chunk)
			_ = MatchesKeyID(chunk, "ctrl+c")
		}
		// Bracketed paste markers are re-added around a paste payload and nothing else is added; a valid UTF-8 stream never grows beyond that.
		if joined := strings.Join(out, ""); utf8.Valid(input) && len(joined) > len(input)+len("\x1b[200~\x1b[201~")*(strings.Count(string(input), "\x1b[200~")+1) {
			t.Fatalf("output %q is longer than input %q", joined, input)
		}
	})
}
