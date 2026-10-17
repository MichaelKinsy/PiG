//go:build !race && !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

// mistralLongEventBytes is what readMistralEvents allocates for one event of about size bytes that arrives in 1 KB reads.
func mistralLongEventBytes(t *testing.T, size int) uint64 {
	body := []byte(`data: {"choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", size) + `"}}]}` + "\n\n")
	var chunks [][]byte
	for rest := body; len(rest) > 0; {
		n := min(1024, len(rest))
		chunks = append(chunks, rest[:n])
		rest = rest[n:]
	}
	return allocatedBytes(func() {
		events := 0
		loop := &mistralEventLoop{reader: &chunkListReader{chunks: chunks}}
		if err := loop.run(func(raw json.RawMessage) error {
			if len(raw) < size {
				t.Fatalf("event has %d bytes, want over %d", len(raw), size)
			}
			events++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if events != 1 {
			t.Fatalf("read %d events, want 1", events)
		}
	})
}

// An event that spans many reads costs memory in proportion to its size: the reader appends each read to its buffer and searches only the bytes that can still start a boundary, instead of concatenating the whole buffer and searching it from the start on every read.
func TestMistralReaderLongEventIsLinear(t *testing.T) {
	small, large := mistralLongEventBytes(t, 64<<10), mistralLongEventBytes(t, 640<<10)
	ratio := float64(large) / float64(small)
	t.Logf("64 KB event: %d B allocated; 640 KB event: %d B (%.1fx)", small, large, ratio)
	if ratio > 20 {
		t.Fatalf("a 10x longer event allocated %.1fx the bytes (%d vs %d); want at most 20x", ratio, large, small)
	}
}
