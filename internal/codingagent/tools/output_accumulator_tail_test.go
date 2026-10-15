package tools

import (
	"math/rand"
	"runtime"
	"strings"
	"testing"
)

// referenceTail is the rolling tail as output-accumulator.ts keeps it: a string that each chunk is concatenated onto,
// trimmed when it passes twice the rolling limit. The accumulator's byte tail must show the same text at every step.
type referenceTail struct {
	maxRollingBytes   int
	text              string
	bytes             int
	startsAtLineStart bool
}

func (r *referenceTail) add(text string) {
	if text == "" {
		return
	}
	r.text += text
	r.bytes += len(text)
	if r.bytes > r.maxRollingBytes*2 {
		if len(r.text) <= r.maxRollingBytes {
			r.bytes = len(r.text)
			return
		}
		start := len(r.text) - r.maxRollingBytes
		for start < len(r.text) && r.text[start]&0xc0 == 0x80 {
			start++
		}
		if start != 0 {
			r.startsAtLineStart = r.text[start-1] == '\n'
		}
		r.text = r.text[start:]
		r.bytes = len(r.text)
	}
}

func (r *referenceTail) snapshotText() string {
	if r.startsAtLineStart {
		return r.text
	}
	if i := strings.IndexByte(r.text, '\n'); i >= 0 {
		return r.text[i+1:]
	}
	return r.text
}

func TestOutputAccumulatorTailMatchesTheStringTail(t *testing.T) {
	pieces := []string{"a", "bc", "line\n", "\n", "héllo wörld", "日本語", "x\ny\nz", "😀", strings.Repeat("q", 40), "tail without newline"}
	for seed := range int64(300) {
		random := rand.New(rand.NewSource(seed))
		maxBytes := 8 + random.Intn(200)
		accumulator := newOutputAccumulator(1<<20, maxBytes, "")
		reference := &referenceTail{maxRollingBytes: max(maxBytes*2, 1), startsAtLineStart: true}
		var decoder = utf8StreamDecoder{stripBOM: true}
		for step := range 120 {
			var chunk []byte
			for range 1 + random.Intn(4) {
				chunk = append(chunk, pieces[random.Intn(len(pieces))]...)
			}
			// Cut a chunk inside a character now and then: the decoder carries the rest to the next one.
			if random.Intn(5) == 0 && len(chunk) > 1 {
				chunk = chunk[:1+random.Intn(len(chunk)-1)]
			}
			if err := accumulator.Append(chunk); err != nil {
				t.Fatal(err)
			}
			reference.add(decoder.decode(chunk, true))
			if got, want := string(accumulator.snapshotText()), reference.snapshotText(); got != want {
				t.Fatalf("seed %d step %d (maxBytes %d): snapshot text\n got %q\nwant %q", seed, step, maxBytes, got, want)
			}
			if len(accumulator.tail) != reference.bytes || accumulator.tailStartsAtLineBoundary != reference.startsAtLineStart {
				t.Fatalf("seed %d step %d: tail bytes %d/%d, line boundary %v/%v", seed, step, len(accumulator.tail), reference.bytes, accumulator.tailStartsAtLineBoundary, reference.startsAtLineStart)
			}
		}
	}
}

// A chunk costs the chunk: the tail is appended to in place, where concatenating onto a string copied the whole tail (up
// to four times maxBytes) for every chunk, 46 us against upstream's 0.8 us.
func TestOutputAccumulatorAppendCostDoesNotGrowWithTheTail(t *testing.T) {
	const chunks = 5000
	chunk := []byte(strings.Repeat("a line of shell output that goes on\n", 2))
	perChunk := func(maxBytes int) float64 {
		accumulator := newOutputAccumulator(1<<30, maxBytes, "")
		// Warm: fill the tail to its rolling limit so the measured appends run against a full tail.
		for range 4*maxBytes/len(chunk) + 16 {
			_ = accumulator.Append(chunk)
		}
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for range chunks {
			_ = accumulator.Append(chunk)
		}
		runtime.ReadMemStats(&after)
		return float64(after.TotalAlloc-before.TotalAlloc) / chunks
	}
	small, large := perChunk(4<<10), perChunk(256<<10)
	t.Logf("bytes allocated per %d-byte chunk: %.0f with a 4 KiB limit, %.0f with a 256 KiB limit", len(chunk), small, large)
	if large > 4*float64(len(chunk))+512 {
		t.Fatalf("a chunk allocates %.0f bytes against a 256 KiB limit: the cost follows the tail", large)
	}
}

// BenchmarkOutputAccumulatorAppend is a chunk of shell output appended to a full default-size tail. Past the limit every raw chunk is also written to the full-output file, as upstream does, and that write system call is most of the time.
func BenchmarkOutputAccumulatorAppend(b *testing.B) {
	chunk := []byte(strings.Repeat("a line of shell output that goes on\n", 2))
	accumulator := NewOutputAccumulator("")
	for range 4*DefaultMaxBytesUpstream/len(chunk) + 16 {
		_ = accumulator.Append(chunk)
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = accumulator.Append(chunk)
	}
}
