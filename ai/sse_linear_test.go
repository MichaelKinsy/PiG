package ai

import (
	"io"
	"strings"
	"testing"
)

type chunkedReader struct {
	data      string
	offset    int
	chunkSize int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := min(len(p), r.chunkSize, len(r.data)-r.offset)
	copy(p[:n], r.data[r.offset:r.offset+n])
	r.offset += n
	return n, nil
}

// Port of Pi main da7e26cc8bca83c15f04e83c9b468e79d8a35cfe (#12451, #12224):
// verify SSE decoder handles CRLF/CR/LF line endings across every chunk size
// and decodes large chunked events in linear time.
func TestSSEDecoderChunkBoundariesAndLargeEvents(t *testing.T) {
	t.Run("handles LF, CRLF, and lone CR at every chunk size", func(t *testing.T) {
		for _, nl := range []string{"\n", "\r\n", "\r"} {
			stream := "event: msg" + nl + "data: one" + nl + "data: two" + nl + nl + ":comment" + nl + "data: three" + nl + nl
			for chunkSize := 1; chunkSize <= len(stream); chunkSize++ {
				dec := newSSEDecoder(&chunkedReader{data: stream, chunkSize: chunkSize})
				var events []serverSentEvent
				for dec.Next() {
					events = append(events, dec.Event())
				}
				if err := dec.Err(); err != nil {
					t.Fatalf("nl=%q chunkSize=%d err=%v", nl, chunkSize, err)
				}
				if len(events) != 2 || events[0].Event != "msg" || events[0].Data != "one\ntwo" || events[1].Data != "three" {
					t.Fatalf("nl=%q chunkSize=%d events=%#v", nl, chunkSize, events)
				}
			}
		}
	})

	t.Run("decodes 1 MiB event in 4 KiB chunks", func(t *testing.T) {
		payload := strings.Repeat("x", 1<<20)
		stream := "data: " + payload + "\n\n"
		dec := newSSEDecoder(&chunkedReader{data: stream, chunkSize: 4096})
		if !dec.Next() {
			t.Fatalf("expected event, err=%v", dec.Err())
		}
		if len(dec.Event().Data) != len(payload) {
			t.Fatalf("data len = %d, want %d", len(dec.Event().Data), len(payload))
		}
		if dec.Next() || dec.Err() != nil {
			t.Fatalf("unexpected extra event or err=%v", dec.Err())
		}
	})
}
