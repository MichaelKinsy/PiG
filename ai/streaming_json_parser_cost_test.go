package ai

import (
	"strings"
	"testing"
	"time"
)

// upstream: packages/ai/test/streaming-json.test.ts "StreamingJsonParser cost" (#9265). The bounds are generous: a parser that re-reads the accumulated text per piece, or recurses per level, takes minutes or overflows the stack.

func TestStreamingJSONParserReadsDeepNestingWithoutRecursionAndInLinearTime(t *testing.T) {
	const depth = 200_000
	started := time.Now()
	var parser StreamingJSONParser
	text := strings.Repeat("[", depth) + "1" + strings.Repeat("]", depth)
	for offset := 0; offset < len(text); offset += 4096 {
		parser.Append(text[offset:min(offset+4096, len(text))])
		parser.Snapshot()
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("deep nesting took %s", elapsed)
	}
	if snapshot := parser.Snapshot(); !snapshot.Defined {
		t.Fatal("the nested array has no value")
	}
}

func TestStreamingJSONParserReadsOneHugePieceInLinearTime(t *testing.T) {
	var piece strings.Builder
	piece.WriteString(`{"items":[`)
	for index := range 400_000 {
		if index > 0 {
			piece.WriteByte(',')
		}
		piece.WriteString(`{"n":1}`)
	}
	piece.WriteString(`]}`)
	started := time.Now()
	var parser StreamingJSONParser
	parser.Append(piece.String())
	if snapshot := parser.Snapshot(); !snapshot.Defined {
		t.Fatal("the document has no value")
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("one huge piece took %s", elapsed)
	}
}

func TestStreamingJSONParserKeepsVeryLargeOpenArraysLinearByLettingThePreviewLag(t *testing.T) {
	started := time.Now()
	var parser StreamingJSONParser
	parser.Append(`{"rows":[`)
	row := strings.Repeat(`{"n":1},`, 500)
	for range 400 {
		parser.Append(row)
		parser.Snapshot()
	}
	parser.Append(`1]}`)
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("a large open array took %s", elapsed)
	}
	if snapshot := parser.Snapshot(); !snapshot.Defined {
		t.Fatal("the document has no value")
	}
}
