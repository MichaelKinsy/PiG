//go:build !race

package ai

import (
	"io"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/internal/testscaling"
)

// openAITextDeltaRecord is a Chat Completions text delta without an escape.
const openAITextDeltaRecord = `data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1760000000,"model":"gpt-x","choices":[{"index":0,"delta":{"content":"sixteen byte txt"},"finish_reason":null}]}` + "\n\n"

// reusedValueReader returns body values in one buffer it overwrites on the next call, as a socket read does: perRead records per
// value, records values in all.
type reusedValueReader struct {
	record  string
	perRead int
	left    int
	buffer  []byte
}

func (reader *reusedValueReader) Read([]byte) (int, error) { panic("chunk reader only") }

func (reader *reusedValueReader) ReadChunk() ([]byte, error) {
	if reader.left == 0 {
		return nil, io.EOF
	}
	n := min(reader.perRead, reader.left)
	reader.left -= n
	reader.buffer = reader.buffer[:0]
	for range n {
		reader.buffer = append(reader.buffer, reader.record...)
	}
	return reader.buffer, nil
}

// decodeOpenAIRecords runs records records, perRead to a body value, through parseSSE's record path and checks that each is
// decoded in place.
func decodeOpenAIRecords(tb testing.TB, record string, records, perRead int) {
	reader := &reusedValueReader{record: record, perRead: perRead, left: records, buffer: make([]byte, 0, perRead*len(record))}
	next := newOpenAIRecordPath(reader, "gpt-x")
	values := 0
	for {
		chunk, model, ok := next()
		if !ok {
			break
		}
		if model != "" || len(chunk.Choices) != 1 {
			tb.Fatalf("record %d decoded as %#v, model %q", values, chunk, model)
		}
		values++
	}
	if values != records {
		tb.Fatalf("decoded %d of %d records in place", values, records)
	}
}

// openAIRecordAllocs is the allocations one more record costs on parseSSE's record path: the difference between 512 and 256 records,
// so the decoder's own setup is not counted.
func openAIRecordAllocs(tb testing.TB, record string, perRead int) float64 {
	run := func(records int) float64 {
		return testing.AllocsPerRun(10, func() { decodeOpenAIRecords(tb, record, records, perRead) })
	}
	return (run(512) - run(256)) / 256
}

// An SSE record costs at most two allocations on parseSSE's record path (S29): the record's text, which the event's data, its raw
// lines and the chunk's unescaped strings are substrings of, and the one string an escaped delta decodes to. The raw line array,
// the chunk's choices, tool calls, functions and indexes, and the model bytes are reused across records, and a model that is the
// configured one is compared in place.
func TestOpenAIStreamRecordAllocs(t *testing.T) {
	for _, test := range []struct {
		name, record string
		want         float64
	}{
		{"text", openAITextDeltaRecord, 1},
		{"escaped-tool-arguments", openAIToolDeltaRecord, 2},
	} {
		for _, perRead := range []int{1, 64} {
			allocs := openAIRecordAllocs(t, test.record, perRead)
			t.Logf("%s, %d records per body value: %.2f allocations per record", test.name, perRead, allocs)
			if allocs > test.want {
				t.Errorf("%s, %d records per body value: %.2f allocations per record, want at most %.0f", test.name, perRead, allocs, test.want)
			}
		}
	}
}

// cpuOf is the least process CPU time of three rounds of runs calls of f on one processor, divided by runs. A search that
// rescans grows CPU time without allocating, so allocations alone do not prove these claims; CPU time, unlike wall time, does
// not grow with host load. Repeating f keeps each round far above the resolution of the process CPU clock.
func cpuOf(t *testing.T, runs int, f func()) time.Duration {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	best := time.Duration(1 << 62)
	for range 3 {
		runtime.GC()
		start := processCPU(t)
		for range runs {
			f()
		}
		best = min(best, processCPU(t)-start)
	}
	return best / time.Duration(runs)
}

// checkCPULinear fails when 8x the work took more than 20x the CPU time, which a linear cost stays well under and a rescan of
// the pending bytes exceeds. The small input runs 8*runs times and the large one runs times, so both rounds take about as long.
func checkCPULinear(t *testing.T, what string, runs int, small, large func()) {
	t.Helper()
	smallCPU, largeCPU := cpuOf(t, 8*runs, small), cpuOf(t, runs, large)
	ratio := float64(largeCPU) / float64(max(smallCPU, time.Nanosecond))
	t.Logf("%s: %v CPU, 8x: %v (%.1fx)", what, smallCPU, largeCPU, ratio)
	testenv.TimingGate(t, ratio > 20, "%s: 8x the input took %.1fx the CPU time (%v vs %v); want at most 20x", what, ratio, largeCPU, smallCPU)
}

// Records that arrive many to a body value, as a socket read of a fast stream returns them, cost in proportion to their number:
// the search for each record's end stops at its line ending instead of scanning the rest of the value for a CR.
func TestOpenAIStreamRecordsInOneValueAreLinear(t *testing.T) {
	checkCPULinear(t, "512 records in one body value", 8,
		func() { decodeOpenAIRecords(t, openAIToolDeltaRecord, 512, 512) },
		func() { decodeOpenAIRecords(t, openAIToolDeltaRecord, 4096, 4096) })
	testscaling.Check(t, testscaling.Case{Name: "records in one body value", Size: 512, Bound: testscaling.Linear, Prepare: func(n int) func() {
		return func() { decodeOpenAIRecords(t, openAIToolDeltaRecord, n, n) }
	}})
}

// A record split across many body values costs in proportion to its size: each value is appended once and only the bytes a
// delimiter can straddle are searched again.
func TestOpenAIStreamSplitRecordIsLinear(t *testing.T) {
	decodeSplit := func(size int) {
		record := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"" + strings.Repeat("x", size) + "\"}}]}\n\n"
		values := 0
		reader := &openAITestChunkReader{read: func() ([]byte, error) {
			if values*1024 >= len(record) {
				return nil, io.EOF
			}
			value := record[values*1024 : min(len(record), (values+1)*1024)]
			values++
			return []byte(value), nil
		}}
		next := newOpenAIRecordPath(reader, "gpt-x")
		if chunk, _, ok := next(); !ok || len(chunk.Choices[0].Delta.Content) != size {
			t.Fatalf("a %d-byte record was not decoded", size)
		}
	}
	checkCPULinear(t, "64 KB record in 1 KB values", 16, func() { decodeSplit(64 << 10) }, func() { decodeSplit(512 << 10) })
	testscaling.Check(t, testscaling.Case{Name: "one record split across 1 KB body values", Size: 64 << 10, Bound: testscaling.Linear, Prepare: func(n int) func() {
		return func() { decodeSplit(n) }
	}})
}

// BenchmarkOpenAIStreamRecords reports parseSSE's record path per record: one record per body value, and 64.
func BenchmarkOpenAIStreamRecords(b *testing.B) {
	for _, test := range []struct {
		name, record string
		perRead      int
	}{
		{"tool-1", openAIToolDeltaRecord, 1},
		{"tool-64", openAIToolDeltaRecord, 64},
		{"text-1", openAITextDeltaRecord, 1},
		{"text-64", openAITextDeltaRecord, 64},
	} {
		b.Run(test.name, func(b *testing.B) {
			const records = 1024
			b.ReportAllocs()
			b.SetBytes(int64(records * len(test.record)))
			for b.Loop() {
				decodeOpenAIRecords(b, test.record, records, test.perRead)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*records), "ns/record")
		})
	}
}
