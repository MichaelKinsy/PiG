package ai

import (
	"bytes"
	"math/rand/v2"
	"strings"
	"testing"
)

// The idle tracker classifies lines in place and finds reasoning markers in one pass. referenceSSEIdleTracker is the tracker it replaced, kept verbatim: it copied every byte into its window and searched each data line once per marker. The differential test requires the same progress for every read and the same reasoning state after it.
type referenceSSEIdleTracker struct {
	line           []byte
	ping           bool
	eventReasoning bool
	reasoning      bool
	afterCR        bool
	kind           sseLineKind
	payload        int
	lineReasoning  bool
}

func (b *referenceSSEIdleTracker) scan(data []byte) (progress bool) {
	for _, c := range data {
		crlf := c == '\n' && b.afterCR
		b.afterCR = c == '\r'
		if crlf {
			continue
		}
		if c != '\n' && c != '\r' {
			b.line = append(b.line, c)
			if len(b.line) == sseLineSniffBytes {
				b.slide()
			}
			continue
		}
		if b.endLine() {
			progress = true
		}
	}
	return progress
}

func (b *referenceSSEIdleTracker) classify() {
	line := b.line
	switch {
	case line[0] == ':':
		b.kind = sseLineComment
	case hasSSEField(line, "event:"):
		b.kind = sseLineEvent
		name := bytes.TrimPrefix(line[len("event:"):], []byte(" "))
		b.ping = string(name) == "ping"
		b.eventReasoning = bytes.Contains(name, []byte("reasoning")) || bytes.Contains(name, []byte("thinking"))
	case hasSSEField(line, "data:"):
		b.kind, b.payload = sseLineData, len("data:")
	default:
		b.kind = sseLineOther
	}
}

func (b *referenceSSEIdleTracker) slide() {
	if b.kind == sseLineUnknown {
		b.classify()
	}
	if b.kind == sseLineData && !b.lineReasoning {
		window := b.line[b.payload:]
		b.lineReasoning = referenceIsReasoningEventData(window, len(window)-sseSniffOverlap)
	}
	b.payload = 0
	b.line = append(b.line[:0], b.line[len(b.line)-sseSniffOverlap:]...)
}

func (b *referenceSSEIdleTracker) endLine() bool {
	if b.kind == sseLineUnknown {
		if len(b.line) == 0 {
			b.ping, b.eventReasoning = false, false
			return false
		}
		b.classify()
	}
	kind, data, lineReasoning := b.kind, b.line[b.payload:], b.lineReasoning
	b.line, b.kind, b.payload, b.lineReasoning = b.line[:0], sseLineUnknown, 0, false
	switch kind {
	case sseLineComment:
		return b.reasoning
	case sseLineData:
		if b.ping {
			return b.reasoning
		}
		b.reasoning = b.eventReasoning || lineReasoning || referenceIsReasoningEventData(data, len(data))
		return true
	}
	return false
}

func referenceIsReasoningEventData(data []byte, limit int) bool {
	for _, marker := range reasoningEventMarkers {
		for _, value := range marker.values {
			if referenceHasJSONMember(data, marker.key, value, limit) {
				return true
			}
		}
	}
	return false
}

func referenceHasJSONMember(data, key, valuePrefix []byte, limit int) bool {
	for offset := 0; ; {
		index := bytes.Index(data[offset:], key)
		if index < 0 || offset+index >= limit {
			return false
		}
		rest := bytes.TrimLeft(data[offset+index+len(key):], " ")
		if len(rest) > 0 && rest[0] == ':' {
			if value, ok := bytes.CutPrefix(bytes.TrimLeft(rest[1:], " "), valuePrefix); ok && !emptyJSONValueStart(valuePrefix, value) {
				return true
			}
		}
		offset += index + len(key)
	}
}

// sseIdleAlphabet builds streams from the pieces the tracker reacts to: line ends of each form, field names, keepalives, every reasoning marker with and without spaces, empty values, and long fillers that cross the sniff window.
var sseIdleAlphabet = []string{
	"\n", "\r", "\r\n", "\n\n", ":", ": keepalive", "event: ", "event: ping", "event: response.reasoning_summary_text.delta", "event:thinking", "data: ", "data:", "id: 1",
	`{"type":"thinking"`, `"type" : "thinking_delta"`, `"type":"signature_delta"`, `"type":"redacted_thinking"`, `"type": "reasoning"`, `"type":"text"`,
	`"reasoning":"x"`, `"reasoning": ""`, `"reasoning_content":"y"`, `"reasoning_content":""`, `"reasoning_text": "z"`, `"reasoning_details":[{}]`, `"reasoning_details": [ ]`,
	`"thought": true`, `"thought":false`, `"type"type": "thinking`, `""reasoning"`, `"t`, `"r`, `":`, " ", ",", "}", "\"",
	strings.Repeat("a", 500), strings.Repeat("b", sseLineSniffBytes-70), strings.Repeat("c", sseSniffOverlap), strings.Repeat("d", 2*sseLineSniffBytes),
}

func TestSSEIdleTrackerMatchesReference(t *testing.T) {
	random := rand.New(rand.NewPCG(7, 8))
	for range 3000 {
		var stream []byte
		for range random.IntN(30) {
			stream = append(stream, sseIdleAlphabet[random.IntN(len(sseIdleAlphabet))]...)
		}
		got, want := &sseIdleBody{}, &referenceSSEIdleTracker{}
		for rest := stream; len(rest) > 0; {
			size := min(len(rest), 1+random.IntN(1+random.IntN(3000)))
			read := bytes.Clone(rest[:size])
			gotProgress := got.scan(read)
			// The tracker must not keep the read: the body reuses its buffer.
			for i := range read {
				read[i] = '#'
			}
			wantProgress := want.scan(rest[:size])
			if gotProgress != wantProgress || got.reasoning != want.reasoning || got.ping != want.ping || got.eventReasoning != want.eventReasoning {
				t.Fatalf("stream %q, read ending at %d: progress=%v reasoning=%v ping=%v eventReasoning=%v, want %v %v %v %v", stream, len(stream)-len(rest)+size, gotProgress, got.reasoning, got.ping, got.eventReasoning, wantProgress, want.reasoning, want.ping, want.eventReasoning)
			}
			rest = rest[size:]
		}
	}
}

func TestSSEIdleReasoningMarkerScanMatchesReference(t *testing.T) {
	random := rand.New(rand.NewPCG(9, 10))
	for range 20000 {
		var data []byte
		for range random.IntN(12) {
			data = append(data, sseIdleAlphabet[random.IntN(len(sseIdleAlphabet))]...)
		}
		limit := random.IntN(len(data)+2) - 1
		if got, want := isReasoningEventData(data, limit), referenceIsReasoningEventData(data, limit); got != want {
			t.Fatalf("isReasoningEventData(%q, %d) = %v, want %v", data, limit, got, want)
		}
	}
}
