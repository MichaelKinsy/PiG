// Ports packages/durable/test/env-line-scan.test.ts.

package env

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
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

// Newlines, ASCII, a byte-order mark, valid multi-byte sequences, and bytes that form invalid or truncated sequences.
var scanPieces = [][]byte{
	{0x0a},
	{0x0a},
	{0x61},
	{0x62, 0x63},
	{0xef, 0xbb, 0xbf},
	{0xc3, 0xa9},
	{0xe2, 0x82, 0xac},
	{0xf0, 0x9f, 0x98, 0x80},
	{0xe2, 0x82},
	{0xff},
	{0x80},
	{0xf0, 0x9f},
	{0x0d, 0x0a},
}

func randomScanFile(next func() float64) []byte {
	var file []byte
	pieces := int(next() * 60)
	for range pieces {
		file = append(file, scanPieces[int(next()*float64(len(scanPieces)))]...)
	}
	return file
}

// decodeWhole is new TextDecoder().decode(bytes): a leading byte-order mark is dropped.
func decodeWhole(data []byte) string {
	return strings.TrimPrefix(jsstring.FromUTF8(data), "\ufeff")
}

// decodeSpan is new TextDecoder("utf-8", { ignoreBOM: from > 0 }).decode(file[from:to]).
func decodeSpan(file []byte, from, to int64) string {
	text := jsstring.FromUTF8(file[from:to])
	if from == 0 {
		return strings.TrimPrefix(text, "\ufeff")
	}
	return text
}

func TestStreamDecoder(t *testing.T) {
	// Node's streaming TextDecoder with BOM handling dropped this U+FEFF, which follows an invalid sequence.
	t.Run("keeps a U+FEFF that does not start the stream", func(t *testing.T) {
		// upstream: packages/durable/test/env-line-scan.test.ts:44
		file := []byte{0xe2, 0x82, 0xef, 0xbb, 0xbf, 0x61}
		decoder := NewStreamDecoder()
		var text strings.Builder
		for _, value := range file {
			text.WriteString(decoder.Decode([]byte{value}))
		}
		text.WriteString(decoder.End())
		if got, want := text.String(), decodeWhole(file); got != want {
			t.Fatalf("decoded %q, want %q", got, want)
		}
		if text.String() != "\ufffd\ufeffa" {
			t.Fatalf("decoded %q, want a replacement character, U+FEFF and a", text.String())
		}
	})

	t.Run("decodes any chunking like decoding the whole stream", func(t *testing.T) {
		// upstream: packages/durable/test/env-line-scan.test.ts:52
		for seed := uint32(1); seed <= 5000; seed++ {
			next := mulberry32(seed)
			file := randomScanFile(next)
			decoder := NewStreamDecoder()
			var text strings.Builder
			for offset := 0; offset < len(file); {
				size := 1 + int(next()*7)
				text.WriteString(decoder.Decode(file[offset:min(offset+size, len(file))]))
				offset += size
			}
			text.WriteString(decoder.End())
			if got, want := text.String(), decodeWhole(file); got != want {
				t.Fatalf("seed %d: decoded %q, want %q", seed, got, want)
			}
		}
	})
}

// Not upstream cases: a split character is held until its bytes arrive, a flush ends one, and a byte-order mark is
// dropped only at the start of the stream.
func TestStreamDecoderHoldsAnIncompleteCharacterUntilItsRemainingBytesArrive(t *testing.T) {
	decoder := NewStreamDecoder()
	for _, step := range []struct {
		chunk []byte
		want  string
	}{
		{[]byte("a\xf0"), "a"},
		{[]byte("\x9f"), ""},
		{[]byte("\x98\x80b"), "😀b"},
		{[]byte("\x80"), "\uFFFD"}, // a stray continuation byte
		{[]byte("\xe4\xb8"), ""},
	} {
		if got := decoder.Decode(step.chunk); got != step.want {
			t.Fatalf("Decode(%x) = %q, want %q", step.chunk, got, step.want)
		}
	}
	if got := decoder.End(); got != "\uFFFD" {
		t.Fatalf("End = %q, want one replacement character", got)
	}
	if got := decoder.End(); got != "" {
		t.Fatalf("second End = %q", got)
	}
}

func TestStreamDecoderDropsAByteOrderMarkOnlyAtTheStartOfTheStream(t *testing.T) {
	decoder := NewStreamDecoder()
	for _, step := range []struct {
		chunk []byte
		want  string
	}{
		{[]byte("\xef\xbb"), ""},
		{[]byte("\xbfa"), "a"},
		{[]byte("\xef\xbb\xbfb"), "\ufeffb"},
	} {
		if got := decoder.Decode(step.chunk); got != step.want {
			t.Fatalf("Decode(%x) = %q, want %q", step.chunk, got, step.want)
		}
	}
}

func TestRangeDecoderKeepsAByteOrderMark(t *testing.T) {
	decoder := NewRangeDecoder()
	if got := decoder.Decode([]byte("\xef\xbb\xbfa")) + decoder.Flush(); got != "\ufeffa" {
		t.Fatalf("decoded %q, want the mark kept", got)
	}
}

func TestStartsWithBom(t *testing.T) {
	for _, step := range []struct {
		bytes []byte
		want  bool
	}{{[]byte("\xef\xbb\xbf"), true}, {[]byte("\xef\xbb"), false}, {nil, false}, {[]byte("abc"), false}} {
		if got := StartsWithBom(step.bytes); got != step.want {
			t.Fatalf("StartsWithBom(%x) = %v", step.bytes, got)
		}
	}
}

func TestLineScanner(t *testing.T) {
	t.Run("agrees with decoding and splitting the whole file", func(t *testing.T) {
		// upstream: packages/durable/test/env-line-scan.test.ts:69
		for seed := uint32(1); seed <= 5000; seed++ {
			next := mulberry32(seed)
			file := randomScanFile(next)
			lines := strings.Split(decodeWhole(file), "\n")
			startLine := int(next() * float64(len(lines)+2))
			var endLine *int64
			if next() >= 0.3 {
				endLine = new(int64(startLine + 1 + int(next()*float64(len(lines)+1))))
			}
			scanner, err := NewLineScanner(int64(startLine), endLine)
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			for offset := 0; offset < len(file); {
				size := 1 + int(next()*7)
				scanner.Push(file[offset:min(offset+size, len(file))])
				offset += size
			}
			scan := scanner.Finish()
			fail := func(format string, args ...any) {
				t.Helper()
				t.Fatalf("seed %d: "+format, append([]any{seed}, args...)...)
			}
			if scan.Newlines != int64(len(lines)-1) {
				fail("newlines %d, want %d", scan.Newlines, len(lines)-1)
			}
			selected := lines[min(startLine, len(lines)):]
			if endLine != nil {
				selected = lines[min(startLine, len(lines)):min(int(*endLine), len(lines))]
			}
			joined := strings.Join(selected, "\n")
			if got := decodeSpan(file, scan.Start, scan.End); got != joined {
				fail("selection %q, want %q", got, joined)
			}
			if scan.SelectedBytes != int64(len(joined)) {
				fail("selectedBytes %d, want %d", scan.SelectedBytes, len(joined))
			}
			if startLine < len(lines) {
				if got := decodeSpan(file, scan.Start, scan.FirstLineEnd); got != lines[startLine] {
					fail("first line %q, want %q", got, lines[startLine])
				}
				if scan.FirstLineBytes != int64(len(lines[startLine])) {
					fail("firstLineBytes %d, want %d", scan.FirstLineBytes, len(lines[startLine]))
				}
				lastLine := len(lines)
				if endLine != nil {
					lastLine = min(int(*endLine), len(lines))
				}
				lastLine--
				lastEnd := int64(len(file))
				if lastLine+1 < len(lines) {
					lastEnd = scan.LastLineStart + int64(bytes.IndexByte(file[scan.LastLineStart:], 0x0a))
				}
				if got := decodeSpan(file, scan.LastLineStart, lastEnd); got != lines[lastLine] {
					fail("last line %q, want %q", got, lines[lastLine])
				}
			} else if scan.Start != int64(len(file)) || scan.End != int64(len(file)) || scan.SelectedBytes != 0 {
				fail("scan %+v, want an empty selection at the end of the file", scan)
			}
		}
	})

	t.Run("rejects empty or invalid ranges", func(t *testing.T) {
		// upstream: packages/durable/test/env-line-scan.test.ts:102
		for _, step := range []struct {
			start int64
			end   *int64
		}{{2, new(int64(2))}, {-1, nil}, {0, new(int64(0))}, {1, new(int64(maxSafeInteger + 1))}} {
			if _, err := NewLineScanner(step.start, step.end); err == nil {
				t.Fatalf("NewLineScanner(%d, %v) succeeded", step.start, step.end)
			}
		}
		// Upstream's fractional start line (1.5) cannot be passed to an int64, so the type rejects it.
	})
}
