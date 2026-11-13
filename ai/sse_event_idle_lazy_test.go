package ai

import (
	"strings"
	"testing"
)

// Only a keepalive asks whether the stream is reasoning, so a stream of text events with no keepalive is never searched for reasoning markers, however its events are spread over reads, and a keepalive costs one search of the one data line before it. The progress each read reports is the reference tracker's (TestSSEIdleTrackerMatchesReference); this pins the count of searches.
func TestSSEIdleTrackerSearchesForMarkersOnlyWhenAKeepaliveAsks(t *testing.T) {
	const events = 100
	const text = `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"content":"word "},"finish_reason":null}]}` + "\n\n"
	const reasoning = `data: {"choices":[{"delta":{"reasoning_content":"hm"}}]}` + "\n\n"
	const keepalive = ": keepalive\n\n"
	// scan feeds one read and then overwrites it, as the body's reader reuses its buffer.
	scan := func(body *sseIdleBody, read string) bool {
		buffer := []byte(read)
		progress := body.scan(buffer)
		for i := range buffer {
			buffer[i] = '#'
		}
		return progress
	}
	for name, reads := range map[string][]string{
		"one read":             {strings.Repeat(text, events)},
		"an event per read":    strings.SplitAfter(strings.Repeat(text, events), "\n\n")[:events],
		"reads that cut lines": strings.SplitAfter(strings.Repeat(text, events), `"choices"`),
	} {
		t.Run(name, func(t *testing.T) {
			body := &sseIdleBody{}
			progressed := false
			for _, read := range reads {
				progressed = scan(body, read) || progressed
			}
			if !progressed || body.markerScans != 0 {
				t.Fatalf("%d text events: progress=%v after %d marker searches, want progress and no search", events, progressed, body.markerScans)
			}
			// The keepalive asks about the last text event, in a read of its own: not reasoning, so no progress, and one search.
			if scan(body, keepalive) || body.markerScans != 1 {
				t.Fatalf("keepalive after text: %d marker searches, want 1 and no progress", body.markerScans)
			}
			// A second keepalive has its answer.
			if scan(body, keepalive) || body.markerScans != 1 {
				t.Fatalf("second keepalive: %d marker searches, want 1 and no progress", body.markerScans)
			}
			// A reasoning event followed by text in the same read is not searched; the one a keepalive follows is, also in a later read.
			if !scan(body, reasoning+text+reasoning) || body.markerScans != 1 {
				t.Fatalf("events without a keepalive: %d marker searches, want 1", body.markerScans)
			}
			if !scan(body, keepalive) || body.markerScans != 2 {
				t.Fatalf("keepalive after reasoning: %d marker searches, want progress and 2", body.markerScans)
			}
			if !scan(body, reasoning+keepalive+text+keepalive) || body.markerScans != 4 {
				t.Fatalf("a keepalive after each of two events: %d marker searches, want 4", body.markerScans)
			}
			if body.isReasoning() || body.markerScans != 4 {
				t.Fatalf("after text and its keepalive: reasoning=%v with %d marker searches, want false and 4", body.isReasoning(), body.markerScans)
			}
		})
	}
}
