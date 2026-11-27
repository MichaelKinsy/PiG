package ai

import (
	"reflect"
	"testing"
)

// Ports packages/ai/test/sse.test.ts (Pi da7e26cc8, #12451).
func TestSseDecoderUpstream(t *testing.T) {
	decode := func(text string, chunkSize int) []ServerSentEvent {
		dec := NewSseDecoder()
		raw := []byte(text)
		var out []ServerSentEvent
		for i := 0; i < len(raw); i += chunkSize {
			end := min(i+chunkSize, len(raw))
			out = append(out, dec.Push(raw[i:end])...)
		}
		out = append(out, dec.End()...)
		return out
	}

	t.Run("gives the same events for every chunking and line ending", func(t *testing.T) {
		evMsg := "message"
		want := []ServerSentEvent{
			{Event: &evMsg, Data: "a\nb", Raw: []string{"event: message", "data: a", "data: b"}},
			{Event: nil, Data: "héllo 😀", Raw: []string{": ignore", "data: héllo 😀"}},
		}
		for _, eol := range []string{"\n", "\r\n", "\r"} {
			text := "event: message" + eol + "data: a" + eol + "data: b" + eol + eol +
				": ignore" + eol + "data: héllo 😀" + eol + eol
			for _, size := range []int{1, 2, 3, 7, 100} {
				got := decode(text, size)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("eol=%q size=%d:\n got %#v\nwant %#v", eol, size, got, want)
				}
			}
		}
	})

	t.Run("does not end an event at a CRLF split across chunks", func(t *testing.T) {
		dec := NewSseDecoder()
		if got := dec.Push([]byte("data: a\r")); len(got) != 0 {
			t.Fatalf("first push = %#v, want empty", got)
		}
		if got := dec.Push([]byte("\ndata: b\r")); len(got) != 0 {
			t.Fatalf("second push = %#v, want empty", got)
		}
		got := dec.Push([]byte("\n\r\n"))
		got = append(got, dec.End()...)
		want := []ServerSentEvent{{Event: nil, Data: "a\nb", Raw: []string{"data: a", "data: b"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("does not carry an empty event name into the next event", func(t *testing.T) {
		got := decode("event:\n\ndata: x\n\n", 100)
		want := []ServerSentEvent{{Event: nil, Data: "x", Raw: []string{"data: x"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})

	t.Run("drops comment lines of events without data", func(t *testing.T) {
		got := decode(": ping\n\ndata: x\n\n", 100)
		want := []ServerSentEvent{{Event: nil, Data: "x", Raw: []string{"data: x"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	})
}
