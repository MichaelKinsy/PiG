//go:build !race

package ai

import (
	"io"
	"testing"
)

// decodeOpenAIToolDeltas runs records tool-call deltas, one body value each, through the decoder.
func decodeOpenAIToolDeltas(tb testing.TB, records int) {
	read := 0
	reader := &openAITestChunkReader{read: func() ([]byte, error) {
		if read == records {
			return nil, io.EOF
		}
		read++
		return []byte(openAIToolDeltaRecord), nil
	}}
	decoder := newOpenAIStreamDecoder(reader, nil, nil)
	values := 0
	for decoder.Next() {
		values++
	}
	if values != records {
		tb.Fatalf("decoded %d of %d records: %v", values, records, decoder.Err())
	}
}

// A record whose text holds an escape is checked for a top-level "error" member without decoding its members, and its bytes are framed in place: it costs the event's text, its raw line list and the test reader's value, not a map of copied member values and a copy per framing layer.
func TestOpenAIStreamDecoderEscapedRecordAllocs(t *testing.T) {
	const records = 64
	allocs := testing.AllocsPerRun(20, func() { decodeOpenAIToolDeltas(t, records) }) / records
	t.Logf("%.1f allocations per escaped record", allocs)
	if allocs > 4 {
		t.Fatalf("an escaped record costs %.1f allocations, want at most 4 (the reader's value, the event text, its raw lines)", allocs)
	}
}

func BenchmarkOpenAIStreamDecoderToolDeltas(b *testing.B) {
	const records = 256
	b.ReportAllocs()
	b.SetBytes(int64(records * len(openAIToolDeltaRecord)))
	for b.Loop() {
		decodeOpenAIToolDeltas(b, records)
	}
}
