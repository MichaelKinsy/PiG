package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

type chunkedByteSource struct {
	data      []byte
	offset    int
	chunkSize int
}

func (s *chunkedByteSource) Close() error { return nil }

func (s *chunkedByteSource) Next(context.Context) ([]byte, bool, error) {
	if s.offset >= len(s.data) {
		return nil, false, nil
	}
	end := min(s.offset+s.chunkSize, len(s.data))
	out := s.data[s.offset:end]
	s.offset = end
	return out, true, nil
}

// Port of Pi main da7e26cc8bca83c15f04e83c9b468e79d8a35cfe (#12451):
// DecodeJsonl reads a multi-MiB line delivered in 4 KiB chunks in linear time.
func TestDecodeJsonlLongLineScalesLinearly(t *testing.T) {
	payload := []byte(`{"v":"` + strings.Repeat("a", 8<<20) + `"}` + "\n")
	run := func(chunk int) time.Duration {
		best := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			dec := DecodeJsonl(&chunkedByteSource{data: payload, chunkSize: chunk})
			val, ok, err := dec.Next(t.Context())
			if err != nil || !ok || val == nil {
				t.Fatalf("chunk=%d val=%v ok=%v err=%v", chunk, val, ok, err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	whole := run(len(payload))
	pieces := run(4 << 10)
	if pieces > 10*whole+60*time.Millisecond {
		t.Fatalf("8 MiB JSONL line in 4 KiB chunks took %v vs whole %v (quadratic rescan/copy)", pieces, whole)
	}
}
