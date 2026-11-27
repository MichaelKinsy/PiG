package mcp

import (
	"io"
	"strings"
	"testing"
	"time"
)

type sseChunkReader struct {
	data      string
	offset    int
	chunkSize int
}

func (r *sseChunkReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := min(len(p), r.chunkSize, len(r.data)-r.offset)
	copy(p[:n], r.data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

// Port of Pi main da7e26cc8bca83c15f04e83c9b468e79d8a35cfe (#12451, #12224).
func TestConsumeSSEStreamLargeEventAndLineLimit(t *testing.T) {
	t.Run("consumes 16 MiB event in 4 KiB chunks in linear time", func(t *testing.T) {
		payload := strings.Repeat("a", 16<<20)
		stream := "data: " + payload + "\n\n"
		run := func(chunk int) time.Duration {
			best := time.Duration(1 << 62)
			for range 3 {
				var got string
				start := time.Now()
				err := ConsumeSSEStream(&sseChunkReader{data: stream, chunkSize: chunk}, ConsumeSSEOptions{
					MaxEventBytes: 32 << 20,
					OnEvent:       func(ev SSEEvent) { got = ev.Data },
				})
				if err != nil || len(got) != len(payload) {
					t.Fatalf("chunk=%d err=%v gotLen=%d", chunk, err, len(got))
				}
				best = min(best, time.Since(start))
			}
			return best
		}
		whole := run(len(stream))
		pieces := run(4 << 10)
		if pieces > 10*whole+60*time.Millisecond {
			t.Fatalf("16 MiB MCP SSE event in 4 KiB chunks took %v vs whole %v", pieces, whole)
		}
	})

	t.Run("rejects an unterminated line exceeding MaxEventBytes", func(t *testing.T) {
		stream := "data: " + strings.Repeat("b", 2048)
		err := ConsumeSSEStream(&sseChunkReader{data: stream, chunkSize: 256}, ConsumeSSEOptions{
			MaxEventBytes: 1024,
			OnEvent:       func(SSEEvent) {},
		})
		if err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("err = %v, want size limit error", err)
		}
	})
}
