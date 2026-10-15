//go:build !race

package ai

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"
)

// sliceChunkSource returns body in reads of size bytes, then io.EOF.
type sliceChunkSource struct {
	body []byte
	size int
}

func (source *sliceChunkSource) ReadChunk() ([]byte, error) {
	if len(source.body) == 0 {
		return nil, io.EOF
	}
	n := min(source.size, len(source.body))
	data := source.body[:n]
	source.body = source.body[n:]
	return data, nil
}

// allocatedBytes is what work allocates on the heap.
func allocatedBytes(work func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	work()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// anthropicLongEventBytes is what the SSE reader allocates for one event of about size bytes that arrives in 1 KB reads, as a large tool input does when a
// gateway sends it whole.
func anthropicLongEventBytes(t *testing.T, size int) uint64 {
	body := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"" + strings.Repeat("x", size) + "\"}}\n\n")
	return allocatedBytes(func() {
		reader := &anthropicSSEReader{ctx: context.Background(), source: &sliceChunkSource{body: body, size: 1024}, suspend: func() {}}
		events := 0
		for {
			event, ok, err := reader.next()
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				break
			}
			if len(event.Data) < size {
				t.Fatalf("event data has %d bytes, want over %d", len(event.Data), size)
			}
			events++
		}
		if events != 1 {
			t.Fatalf("read %d events, want 1", events)
		}
	})
}

// An event that spans many reads costs memory in proportion to its size: the reader appends each read to its line buffer and searches only the new bytes, instead of concatenating the whole buffer again and searching it from the start on every read.
func TestAnthropicSSEReaderLongEventIsLinear(t *testing.T) {
	small, large := anthropicLongEventBytes(t, 64<<10), anthropicLongEventBytes(t, 640<<10)
	ratio := float64(large) / float64(small)
	t.Logf("64 KB event: %d B allocated; 640 KB event: %d B (%.1fx)", small, large, ratio)
	if ratio > 20 {
		t.Fatalf("a 10x longer event allocated %.1fx the bytes (%d vs %d); want at most 20x", ratio, large, small)
	}
}
