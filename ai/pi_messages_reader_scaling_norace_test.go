//go:build !race

package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// piMessagesLongEventBytes is what readPiMessagesEvents allocates for one event of about size bytes that arrives in 1 KB reads.
func piMessagesLongEventBytes(t *testing.T, size int) uint64 {
	body := []byte(`data: {"type":"text_delta","delta":"` + strings.Repeat("x", size) + `"}` + "\r\n\r\n")
	var chunks [][]byte
	for rest := body; len(rest) > 0; {
		n := min(1024, len(rest))
		chunks = append(chunks, rest[:n])
		rest = rest[n:]
	}
	return allocatedBytes(func() {
		events := 0
		executor := &continuationExecutor{}
		executor.run(func(turn *continuationTurn) {
			loop := &piMessagesEventLoop{reader: &chunkListReader{chunks: chunks}, turn: turn}
			_ = loop.run(func(raw json.RawMessage) (bool, error) {
				if len(raw) < size {
					t.Errorf("event has %d bytes, want over %d", len(raw), size)
				}
				events++
				return true, nil
			})
		})
		if events != 1 {
			t.Fatalf("read %d events, want 1", events)
		}
	})
}

// An event that spans many reads costs memory in proportion to its size: the reader appends each read to its buffer, replaces CRLF in the new text only and searches only the new bytes, instead of concatenating, replacing over and searching the whole buffer on every read.
func TestPiMessagesReaderLongEventIsLinear(t *testing.T) {
	small, large := piMessagesLongEventBytes(t, 64<<10), piMessagesLongEventBytes(t, 640<<10)
	ratio := float64(large) / float64(small)
	t.Logf("64 KB event: %d B allocated; 640 KB event: %d B (%.1fx)", small, large, ratio)
	if ratio > 20 {
		t.Fatalf("a 10x longer event allocated %.1fx the bytes (%d vs %d); want at most 20x", ratio, large, small)
	}
}
