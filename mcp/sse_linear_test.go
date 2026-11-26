package mcp

import (
	"io"
	"strings"
	"testing"
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
	t.Run("consumes 1 MiB event in 4 KiB chunks", func(t *testing.T) {
		payload := strings.Repeat("a", 1<<20)
		stream := "data: " + payload + "\n\n"
		var got string
		err := ConsumeSSEStream(&sseChunkReader{data: stream, chunkSize: 4096}, ConsumeSSEOptions{
			MaxEventBytes: 2 << 20,
			OnEvent: func(ev SSEEvent) { got = ev.Data },
		})
		if err != nil {
			t.Fatalf("ConsumeSSEStream failed: %v", err)
		}
		if len(got) != len(payload) {
			t.Fatalf("got len = %d, want %d", len(got), len(payload))
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
