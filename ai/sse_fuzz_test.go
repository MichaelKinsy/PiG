package ai

import (
	"bytes"
	"io"
	"reflect"
	"testing"
	"testing/iotest"
)

func decodeAllSSE(reader io.Reader) ([]serverSentEvent, error) {
	decoder := newSSEDecoder(reader)
	var events []serverSentEvent
	for decoder.Next() {
		events = append(events, decoder.Event())
	}
	return events, decoder.Err()
}

// A stream decodes to the same events however the transport cuts it into reads: whole, one byte at a time, or in two chunks at any offset.
func FuzzSSEDecoderIsIndependentOfChunking(f *testing.F) {
	for _, seed := range []string{
		"data: a\n\ndata: b\n\n", "event: x\ndata: 1\ndata: 2\n\n", ": comment\ndata:no space\n\n", "data: a\r\n\r\ndata: b\r\n\r\n", "data: a\rdata: b\r\r",
		"data\n\n", "event: only\n\n", "data: a\n", "\n\n\n", "data: \xff\xfe\n\n", "id: 1\nretry: 5\ndata: x\n\n", "data: " + string(bytes.Repeat([]byte("x"), 70000)) + "\n\n",
	} {
		f.Add([]byte(seed), 3)
	}
	f.Fuzz(func(t *testing.T, input []byte, cut int) {
		want, wantErr := decodeAllSSE(bytes.NewReader(input))
		if cut < 0 {
			cut = -cut
		}
		cut %= len(input) + 1
		for name, reader := range map[string]io.Reader{
			"one byte": iotest.OneByteReader(bytes.NewReader(input)),
			"two part": io.MultiReader(bytes.NewReader(input[:cut]), bytes.NewReader(input[cut:])),
			"half":     iotest.HalfReader(bytes.NewReader(input)),
		} {
			got, gotErr := decodeAllSSE(reader)
			if !reflect.DeepEqual(got, want) || (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("%s: %d events (err %v), whole input gives %d (err %v)\ninput %q", name, len(got), gotErr, len(want), wantErr, input)
			}
		}
	})
}
