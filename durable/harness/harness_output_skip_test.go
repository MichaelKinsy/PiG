// Ports packages/durable/test/harness-output-skip.test.ts.

package harness

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/durable/env"
)

// mulberry32 is the deterministic PRNG of the upstream tests, so a seed builds the same input as there.
func mulberry32(seed uint32) func() float64 {
	state := seed
	return func() float64 {
		state += 0x6d2b79f5
		t := state
		t = (t ^ t>>15) * (t | 1)
		t ^= t + (t^t>>7)*(t|61)
		return float64(t^t>>14) / 4294967296
	}
}

// Decoded shell output: ASCII, multi-byte and astral characters, replacement characters, CR/LF, tabs, and control
// characters that sanitizing removes after bounding.
var skipAlphabet = []string{"a", "b", "z", " ", "\n", "\n", "\n", "\r\n", "\t", "é", "€", "😀", "\ufffd", "\x01", "\x1b"}

func randomOutputChunk(next func() float64) string {
	length := int(next() * 12)
	var text strings.Builder
	for range length {
		text.WriteString(skipAlphabet[int(next()*float64(len(skipAlphabet)))])
	}
	return text.String()
}

func measureOutput(text string) env.ShellOutputSkip {
	return env.ShellOutputSkip{Bytes: len(text), Newlines: strings.Count(text, "\n"), EndsWithNewline: strings.HasSuffix(text, "\n")}
}

// exceedsWindow reports whether text proves that everything before it is outside the tail window.
func exceedsWindow(text string, limits OutputLimits) bool {
	measured := measureOutput(text)
	return measured.Bytes > limits.MaxBytes || measured.Newlines > limits.MaxLines
}

// codePointBoundaries are the offsets of text where a cut never splits a character.
func codePointBoundaries(text string) []int {
	result := []int{0}
	for offset := 0; offset < len(text); {
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
		result = append(result, offset)
	}
	return result
}

func randomLimits(next func() float64) OutputLimits {
	return OutputLimits{MaxBytes: 1 + int(next()*40), MaxLines: 1 + int(next()*5), Retain: "tail"}
}

// checkSkipSeed feeds random output to one buffer in full and to another through a reference skipper that follows the
// contract of ShellOutputInfo.Skipped: it holds undelivered text, and when it flushes, it may replace a prefix of it by
// counts if the rest exceeds the window. Snapshots must agree whenever both buffers have seen the same output.
func checkSkipSeed(t *testing.T, seed uint32) {
	t.Helper()
	next := mulberry32(seed)
	limits := randomLimits(next)
	full := NewOutputBuffer(limits)
	skipping := NewOutputBuffer(limits)
	pending := ""
	flush := func() {
		t.Helper()
		if pending == "" {
			return
		}
		var cuts []int
		for _, cut := range codePointBoundaries(pending) {
			if cut > 0 && exceedsWindow(pending[cut:], limits) {
				cuts = append(cuts, cut)
			}
		}
		if len(cuts) > 0 && next() < 0.7 {
			cut := cuts[int(next()*float64(len(cuts)))]
			if _, err := skipping.PushStringSkipping(pending[cut:], measureOutput(pending[:cut])); err != nil {
				t.Fatal(err)
			}
		} else {
			skipping.PushString(pending)
		}
		pending = ""
		// Progress snapshots happen between deliveries and compact stored chunks.
		if next() < 0.5 {
			skipping.Snapshot()
		}
		if got, want := skipping.Snapshot(), full.Snapshot(); got != want {
			t.Fatalf("seed %d: skipping snapshot %+v, want %+v", seed, got, want)
		}
	}
	chunks := int(next() * 40)
	for range chunks {
		chunk := randomOutputChunk(next)
		full.PushString(chunk)
		if next() < 0.3 {
			full.Snapshot()
		}
		pending += chunk
		if next() < 0.3 {
			flush()
		}
	}
	flush()
	full.End()
	skipping.End()
	if got, want := skipping.Snapshot(), full.Snapshot(); got != want {
		t.Fatalf("seed %d: final skipping snapshot %+v, want %+v", seed, got, want)
	}
}

func TestOutputBufferSkippedOutput(t *testing.T) {
	// Progress commits snapshot at arbitrary moments; snapshots compact what is stored and must never change the window.
	// A compaction to exactly the kept window lost the character that decides where a later window's first line starts.
	t.Run("keeps the same tail whenever progress snapshots happen", func(t *testing.T) {
		// upstream: packages/durable/test/harness-output-skip.test.ts:100
		for seed := uint32(1); seed <= 3000; seed++ {
			next := mulberry32(seed)
			limits := randomLimits(next)
			plain := NewOutputBuffer(limits)
			sampled := NewOutputBuffer(limits)
			chunks := int(next() * 30)
			for range chunks {
				chunk := randomOutputChunk(next)
				plain.PushString(chunk)
				sampled.PushString(chunk)
				if next() < 0.4 {
					sampled.Snapshot()
				}
			}
			if got, want := sampled.Snapshot(), plain.Snapshot(); got != want {
				t.Fatalf("seed %d: sampled snapshot %+v, want %+v", seed, got, want)
			}
		}
	})

	t.Run("matches the full stream for every legal skip pattern", func(t *testing.T) {
		// upstream: packages/durable/test/harness-output-skip.test.ts:121
		for seed := uint32(1); seed <= 3000; seed++ {
			checkSkipSeed(t, seed)
		}
	})

	t.Run("counts skipped bytes, lines and the final newline exactly", func(t *testing.T) {
		// upstream: packages/durable/test/harness-output-skip.test.ts:125
		limits := OutputLimits{MaxBytes: 1000, MaxLines: 2, Retain: "tail"}
		full := NewOutputBuffer(limits)
		skipping := NewOutputBuffer(limits)
		// The skipped text ends without a newline, so its last line continues in the delivered text.
		omitted := "one\ntwo\nthr"
		kept := "ee\nfour\nfive\nsix"
		full.PushString(omitted + kept)
		if _, err := skipping.PushStringSkipping(kept, measureOutput(omitted)); err != nil {
			t.Fatal(err)
		}
		if got, want := skipping.Snapshot(), full.Snapshot(); got != want {
			t.Fatalf("skipping snapshot %+v, want %+v", got, want)
		}
		if got, want := skipping.Snapshot(), (BoundedOutput{Text: "five\nsix", DroppedBytes: 19, DroppedLines: 4}); got != want {
			t.Fatalf("snapshot %+v, want %+v", got, want)
		}
	})

	t.Run("refuses skips for head retention", func(t *testing.T) {
		// upstream: packages/durable/test/harness-output-skip.test.ts:138
		buffer := NewOutputBuffer(OutputLimits{MaxBytes: 10, MaxLines: 2, Retain: "head"})
		_, err := buffer.PushStringSkipping("x\ny\nz\n", env.ShellOutputSkip{Bytes: 3, Newlines: 1, EndsWithNewline: true})
		if err == nil || !strings.Contains(err.Error(), "tail retention") {
			t.Fatalf("error = %v, want one naming tail retention", err)
		}
	})
}

// Not an upstream case: a byte chunk delivered after an omission ends an incomplete character before it, as a string
// push does, and counts the omission once.
func TestOutputBufferSkippedOutputEndsAnIncompleteCharacterBeforeTheOmission(t *testing.T) {
	limits := OutputLimits{MaxBytes: 4, MaxLines: 10, Retain: "tail"}
	buffer := NewOutputBuffer(limits)
	buffer.PushBytes([]byte{0xe2, 0x82})
	if _, err := buffer.PushBytesSkipping([]byte("tail!\n"), env.ShellOutputSkip{Bytes: 9, Newlines: 1, EndsWithNewline: true}); err != nil {
		t.Fatal(err)
	}
	buffer.End()
	snapshot := buffer.Snapshot()
	// One replacement character (3 bytes) for the held bytes, the 9 skipped bytes, then "tail!\n" cut to the window.
	if want := (BoundedOutput{Text: "il!\n", DroppedBytes: 3 + 9 + 6 - 4, DroppedLines: 1}); snapshot != want {
		t.Fatalf("snapshot %+v, want %+v", snapshot, want)
	}
}
