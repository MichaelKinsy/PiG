package mcp_test

import (
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/streamable-http.test.ts "recognizes CR, LF, and CRLF line endings, including CRLF split across chunks" and
// "dispatches CR-terminated events while the stream stays open" (Pi b64e6badd, #11775).
func TestConsumeSSEStreamRecognizesCRLFAndCRLineEndingsIncludingCRLFSplitAcrossChunks(t *testing.T) {
	parse := func(chunks ...string) []mcp.SSEEvent {
		t.Helper()
		var events []mcp.SSEEvent
		if err := mcp.ConsumeSSEStream(&chunkReader{chunks: chunks}, mcp.ConsumeSSEOptions{OnEvent: func(e mcp.SSEEvent) { events = append(events, e) }}); err != nil {
			t.Fatal(err)
		}
		return events
	}
	expected := []mcp.SSEEvent{{ID: "1", Data: "a\nb"}, {Data: "c"}}
	for _, separator := range []string{"\n", "\r\n", "\r"} {
		text := strings.Join([]string{"id: 1", "data: a", "data: b", "", "data: c", "", ""}, separator)
		if got := parse(text); !reflect.DeepEqual(got, expected) {
			t.Errorf("%q as one chunk: %#v, want %#v", separator, got, expected)
		}
		// One character per chunk splits every CRLF between chunks.
		singles := make([]string, 0, len(text))
		for _, char := range text {
			singles = append(singles, string(char))
		}
		if got := parse(singles...); !reflect.DeepEqual(got, expected) {
			t.Errorf("%q one character per chunk: %#v, want %#v", separator, got, expected)
		}
	}
	if got := parse("id: 1\rdata: a\r\ndata: b\n\r", "\ndata: c\r\r"); !reflect.DeepEqual(got, expected) {
		t.Errorf("mixed endings: %#v, want %#v", got, expected)
	}
}

func TestConsumeSSEStreamDispatchesCRTerminatedEventsWhileTheStreamStaysOpen(t *testing.T) {
	reader, writer := io.Pipe()
	received := make(chan mcp.SSEEvent, 1)
	done := make(chan error, 1)
	go func() {
		done <- mcp.ConsumeSSEStream(reader, mcp.ConsumeSSEOptions{OnEvent: func(e mcp.SSEEvent) { received <- e }})
	}()
	if _, err := writer.Write([]byte("data: first\r\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-received:
		if event != (mcp.SSEEvent{Data: "first"}) {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a CR-terminated event was not dispatched while the stream stayed open")
	}
	_ = writer.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
