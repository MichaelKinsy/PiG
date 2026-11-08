package ai

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"weak"
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

// upstream streaming-json.test.ts "is collected with its snapshots once the stream is dropped" (#9265): a ToolCallArgumentsStream that read a hundred pieces
// is unreachable once the caller lets it go. Snapshots are maps and cannot be weakly referenced; each is reachable only through the stream or
// its caller, so the stream being collected is the property.
func TestToolCallArgumentsStreamIsCollectedOnceTheCallerDropsIt(t *testing.T) {
	stream := func() weak.Pointer[ToolCallArgumentsStream] {
		args := NewToolCallArgumentsStream()
		for index := range 100 {
			args.Append(fmt.Sprintf(`{"a%d":"%d`, index, index))
		}
		return weak.Make(args)
	}()
	collectedWithin(t, 10*time.Second, func() int {
		if stream.Value() != nil {
			return 1
		}
		return 0
	})
}
