package ai

import (
	"io"
	"strings"
	"testing"
	"time"
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
// verify SSE decoders handle CRLF/CR/LF line endings across every chunk size
// and decode large chunked events in linear time (ratio-bounded vs single-chunk).
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

	t.Run("decodes 16 MiB event in 4 KiB chunks in linear time", func(t *testing.T) {
		payload := strings.Repeat("x", 16<<20)
		stream := "data: " + payload + "\n\n"
		run := func(chunk int) time.Duration {
			best := time.Duration(1 << 62)
			for range 3 {
				start := time.Now()
				dec := newSSEDecoder(&chunkedReader{data: stream, chunkSize: chunk})
				if !dec.Next() || len(dec.Event().Data) != len(payload) || dec.Next() || dec.Err() != nil {
					t.Fatalf("chunk=%d decode failed: err=%v", chunk, dec.Err())
				}
				best = min(best, time.Since(start))
			}
			return best
		}
		whole := run(len(stream))
		pieces := run(4 << 10)
		if pieces > 10*whole+60*time.Millisecond {
			t.Fatalf("16 MiB SSE event in 4 KiB chunks took %v vs whole %v", pieces, whole)
		}
	})

	t.Run("codexSSEFrames decodes 16 MiB event in 4 KiB chunks in linear time", func(t *testing.T) {
		payload := strings.Repeat("y", 16<<20)
		stream := []byte("data: " + payload + "\n\n")
		run := func(chunk int) time.Duration {
			best := time.Duration(1 << 62)
			for range 3 {
				start := time.Now()
				var frames codexSSEFrames
				var dec codexTextDecoder
				for i := 0; i < len(stream); i += chunk {
					frames.add(&dec, stream[i:min(i+chunk, len(stream))], false)
				}
				frames.add(&dec, nil, true)
				ev, ok := frames.next()
				if !ok || len(ev) != len(payload) {
					t.Fatalf("chunk=%d ok=%v len=%d", chunk, ok, len(ev))
				}
				best = min(best, time.Since(start))
			}
			return best
		}
		whole := run(len(stream))
		pieces := run(4 << 10)
		if pieces > 10*whole+60*time.Millisecond {
			t.Fatalf("16 MiB Codex SSE event in 4 KiB chunks took %v vs whole %v", pieces, whole)
		}
	})
}
