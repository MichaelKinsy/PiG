package truncate

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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

// A window retains between maxBytes+2 and maxBytes+2+utf8.UTFMax bytes, begins on a UTF-8 character boundary
// for valid input, and holds at most twice that upper bound however much is pushed.
func TestTailWindowKeepsABoundedSuffix(t *testing.T) {
	const (
		maxBytes = 100
		minKeep  = maxBytes + 2
		maxKeep  = maxBytes + 2 + utf8.UTFMax
		maxHeld  = 2 * maxKeep
	)
	check := func(name string, w *TailWindow, wantTotal int) {
		t.Helper()
		win := w.window()
		if len(win) < minKeep || len(win) > maxKeep {
			t.Fatalf("%s: window %d bytes, want in [%d, %d]", name, len(win), minKeep, maxKeep)
		}
		if !utf8.RuneStart(win[0]) {
			t.Fatalf("%s: window starts on UTF-8 continuation byte 0x%02x", name, win[0])
		}
		if len(w.buf) > maxHeld {
			t.Fatalf("%s: held %d bytes, want <= %d", name, len(w.buf), maxHeld)
		}
		if got := w.Result().TotalBytes; got != wantTotal {
			t.Fatalf("%s: total %d, want %d", name, got, wantTotal)
		}
	}

	window := NewTailWindow(maxBytes, 10)
	for range 1000 {
		window.Append(strings.Repeat("0123456789", 50))
	}
	check("large chunks", window, 500_000)

	small := NewTailWindow(maxBytes, 10)
	for range 1000 {
		small.Append("0123456")
	}
	check("small chunks", small, 7_000)
}

// Pi 9e9609216 (#12355): TailWindow.Result must equal TruncateTail on over-long lines followed by blank lines
// and on mid-character windows (with and without a trailing newline, and when the window holds only continuation bytes).
func TestTailWindowMidCharacterEquivalence(t *testing.T) {
	cases := []struct {
		name            string
		stream          string
		maxBytes        int
		maxLines        int
		wantContent     string
		wantOutputLines int
		wantOutputBytes int
		wantPartial     bool
		wantBy          string
	}{
		{
			name:            "over-long non-blank line followed by blank line",
			stream:          "first\n" + strings.Repeat("y", 20) + "END\n\n",
			maxBytes:        10,
			maxLines:        2000,
			wantContent:     "yyyyyyEND\n",
			wantOutputLines: 2,
			wantOutputBytes: 10,
			wantPartial:     true,
			wantBy:          "bytes",
		},
		{
			name:            "Case A: 5x NBSP + newline + space (no trailing newline)",
			stream:          strings.Repeat("\u00a0", 5) + "\n ",
			maxBytes:        5,
			maxLines:        2000,
			wantContent:     " ",
			wantOutputLines: 1,
			wantOutputBytes: 1,
			wantPartial:     false,
			wantBy:          "bytes",
		},
		{
			name:            "Case B: a + 3-byte rune + two newlines at maxBytes 2",
			stream:          "a日\n\n",
			maxBytes:        2,
			maxLines:        2000,
			wantContent:     "\n",
			wantOutputLines: 2,
			wantOutputBytes: 1,
			wantPartial:     true,
			wantBy:          "bytes",
		},
		{
			name:            "5x NBSP + two newlines at maxBytes 5",
			stream:          strings.Repeat("\u00a0", 5) + "\n\n",
			maxBytes:        5,
			maxLines:        2000,
			wantContent:     "",
			wantOutputLines: 1,
			wantOutputBytes: 0,
			wantPartial:     false,
			wantBy:          "bytes",
		},
		{
			name:            "over-50KB U+3000 + two newlines at default limits",
			stream:          strings.Repeat("\u3000", 17070) + "\n\n",
			maxBytes:        DefaultMaxBytes,
			maxLines:        DefaultMaxLines,
			wantContent:     "",
			wantOutputLines: 1,
			wantOutputBytes: 0,
			wantPartial:     false,
			wantBy:          "bytes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewTailWindow(tc.maxBytes, tc.maxLines)
			w.Append(tc.stream)
			got := w.Result()
			want := TruncateTail(tc.stream, tc.maxBytes, tc.maxLines)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("TailWindow.Result() = %+v, want TruncateTail %+v", got, want)
			}
			if got.Content != tc.wantContent || got.OutputLines != tc.wantOutputLines || got.OutputBytes != tc.wantOutputBytes || got.LastLinePartial != tc.wantPartial || got.TruncatedBy != tc.wantBy {
				t.Fatalf("got = %+v, want content=%q outputLines=%d outputBytes=%d partial=%v by=%q",
					got, tc.wantContent, tc.wantOutputLines, tc.wantOutputBytes, tc.wantPartial, tc.wantBy)
			}
		})
	}
}

// Pins both sides of TailWindow's documented trailing-whitespace exception boundary
// (keep = maxBytes+2+utf8.UTFMax-1 = maxBytes+5; with one trailing blank line "\n\n",
// up to maxBytes+2 trailing spaces leave the non-blank prefix inside the retained window,
// while maxBytes+3 trailing spaces push it past the window).
func TestTailWindowTrailingWhitespaceExceptionBoundary(t *testing.T) {
	// maxBytes=8 -> keep=13. "yyx" + 10 spaces + "\n\n" = 15 bytes (> 13, so window actually trims).
	// The last 13 bytes are "x" + 10 spaces + "\n\n": "x" stays inside the window -> matches TruncateTail.
	inside := "yyx" + strings.Repeat(" ", 10) + "\n\n"
	wIn := NewTailWindow(8, 2000)
	wIn.Append(inside)
	if got, want := wIn.Result(), TruncateTail(inside, 8, 2000); !reflect.DeepEqual(got, want) {
		t.Fatalf("inside boundary: TailWindow = %+v, want TruncateTail = %+v", got, want)
	}

	// "yyx" + 11 spaces + "\n\n" = 16 bytes; last 13 bytes are 11 spaces + "\n\n":
	// "x" falls before the window -> TruncateTail keeps 7 spaces + "\n" (partial), TailWindow returns "".
	outside := "yyx" + strings.Repeat(" ", 11) + "\n\n"
	wOut := NewTailWindow(8, 2000)
	wOut.Append(outside)
	gotOut := wOut.Result()
	wantFull := TruncateTail(outside, 8, 2000)
	if wantFull.Content != "       \n" || wantFull.OutputLines != 2 || !wantFull.LastLinePartial {
		t.Fatalf("TruncateTail(outside) = %+v, want 7 spaces + newline, 2 lines, partial", wantFull)
	}
	if gotOut.Content != "" || gotOut.OutputLines != 1 || gotOut.LastLinePartial {
		t.Fatalf("TailWindow(outside) = %+v, want documented exception (empty, 1 line, not partial)", gotOut)
	}
}

// Property-style sweep over small alphabets with multi-byte runes (non-blank and JS whitespace)
// and maxBytes 1..8: every difference from TruncateTail must fall inside the documented trailing-whitespace exception.
func TestTailWindowMatchesTruncateTailOnMultiByteAlphabetSweep(t *testing.T) {
	rng := rand.New(rand.NewPCG(42, 99))
	alpha := []string{"a", " ", "\t", "\n", "\r", "\u00a0", "\u3000", "é", "日", "😀"}
	for maxBytes := 1; maxBytes <= 8; maxBytes++ {
		for _, maxLines := range []int{1, 2, 3, 10} {
			for range 1000 {
				var b strings.Builder
				n := 1 + rng.IntN(24)
				for range n {
					b.WriteString(alpha[rng.IntN(len(alpha))])
				}
				stream := b.String()
				w := NewTailWindow(maxBytes, maxLines)
				for pos := 0; pos < len(stream); {
					step := 1 + rng.IntN(len(stream)-pos)
					w.Append(stream[pos : pos+step])
					pos += step
				}
				got := w.Result()
				want := TruncateTail(stream, maxBytes, maxLines)
				if reflect.DeepEqual(got, want) {
					continue
				}
				// Verify the only permitted difference: want took a partial line whose retained prefix in the aligned window is all JS whitespace.
				win := w.window()
				if w.totalBytes > len(win) {
					i := 0
					for i < len(win) && win[i]&0xC0 == 0x80 {
						i++
					}
					win = win[i:]
				}
				firstNL := strings.IndexByte(string(win), '\n')
				if firstNL < 0 {
					firstNL = len(win)
				}
				if !want.LastLinePartial || got.LastLinePartial || firstNL == 0 || !isBlankLine(win, 0, firstNL) {
					t.Fatalf("unexpected diff maxBytes=%d maxLines=%d stream=%q win=%q:\n  got  %+v\n  want %+v",
						maxBytes, maxLines, stream, string(win), got, want)
				}
			}
		}
	}
}
