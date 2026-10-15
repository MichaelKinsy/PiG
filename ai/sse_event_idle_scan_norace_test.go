//go:build !race

package ai

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// sseIdleScanStream is a megabyte of what the idle tracker reads during a large write: Chat Completions tool-argument deltas whose text carries escapes, with reasoning deltas and keepalives between them, in 4 KB reads.
func sseIdleScanStream() [][]byte {
	var stream strings.Builder
	for stream.Len() < 1<<20 {
		stream.WriteString(`data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1760000000,"model":"gpt-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"    if err := write(\"path\", data); err != nil {\n\t\treturn err\n\t}\n"}}]},"finish_reason":null}]}` + "\n\n")
		stream.WriteString(`data: {"choices":[{"delta":{"reasoning_content":"Considering the next step of the edit."}}]}` + "\n\n")
		stream.WriteString(": keepalive\n\n")
	}
	data := []byte(stream.String())
	var reads [][]byte
	for len(data) > 0 {
		size := min(len(data), 4096)
		reads, data = append(reads, data[:size]), data[size:]
	}
	return reads
}

// fastest is the least wall time of runs of work, the run least disturbed by the rest of the machine.
func fastest(runs int, work func()) time.Duration {
	best := time.Duration(1<<63 - 1)
	for range runs {
		start := time.Now()
		work()
		best = min(best, time.Since(start))
	}
	return best
}

// The tracker finds line ends with a byte search and reads each data line once for its reasoning markers. Its cost is bounded against the floor of any line
// tracker, counting the stream's lines and copying it: a per-byte loop that appends every byte and searches each line once per marker costs over 200 times
// that floor; this tracker costs about 20 times it.
func TestSSEIdleTrackerScanIsNotAPerByteLoop(t *testing.T) {
	reads := sseIdleScanStream()
	scan := fastest(7, func() {
		body := &sseIdleBody{}
		for _, read := range reads {
			body.scan(read)
		}
	})
	buffer := make([]byte, 4096)
	lines := 0
	floor := fastest(7, func() {
		for _, read := range reads {
			lines += bytes.Count(read, []byte{'\n'})
			copy(buffer, read)
		}
	})
	ratio := float64(scan) / float64(floor)
	t.Logf("scan %v, line count (%d lines in 7 runs) and copy %v: %.1fx", scan, lines, floor, ratio)
	if ratio > 60 {
		t.Fatalf("scanning 1 MB of SSE took %v, %.1fx counting its lines and copying it (%v); want at most 60x", scan, ratio, floor)
	}
}

func BenchmarkSSEIdleTrackerScan(b *testing.B) {
	reads := sseIdleScanStream()
	b.SetBytes(int64(len(reads)) * 4096)
	b.ReportAllocs()
	for b.Loop() {
		body := &sseIdleBody{}
		for _, read := range reads {
			body.scan(read)
		}
	}
}
