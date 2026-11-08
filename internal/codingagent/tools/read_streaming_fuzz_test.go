package tools

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

// FuzzReadStreamingMatchesTheWholeFileAlgorithm reads arbitrary bytes, chunk size, offset and limit through the streamed path, through the production whole-file
// path (readTextResult, the path a read without createReadStream and a range that cannot stream take) and, for a range that can stream, through the whole-file
// reference: lines, truncation, counts and the decoding of invalid UTF-8 split at chunk ends must agree. Offsets and limits are any int32 with a fraction, so
// negative and fractional ranges (the whole-file path's) and the edge of the streamed range are read too.
func FuzzReadStreamingMatchesTheWholeFileAlgorithm(f *testing.F) {
	many := repeatLines(3000, func(i int) string { return fmt.Sprintf("line %d é€", i) })
	f.Add([]byte("alpha\nbeta\ngamma"), uint16(1), int32(0), uint8(0), int32(-1), false)
	f.Add([]byte("a\n\n\nb\n"), uint16(3), int32(2), uint8(0), int32(1), true)
	f.Add([]byte("a\xc3\n\xe2\x82\n\xff\xfe\x80\xf0\x9f\x98\n\xed\xa0\x80"), uint16(2), int32(0), uint8(0), int32(-1), false)
	f.Add([]byte(many), uint16(4096), int32(0), uint8(0), int32(-1), false)
	f.Add([]byte(many), uint16(777), int32(2500), uint8(0), int32(10), true)
	f.Add([]byte(many+"\n\n"), uint16(64), int32(1), uint8(0), int32(2999), true)
	f.Add([]byte(many+"\xe2\x82"), uint16(5000), int32(3), uint8(0), int32(2990), true)
	f.Add([]byte("a\nb\n"+strings.Repeat("z", 60*1024)+"\nc"), uint16(4096), int32(0), uint8(0), int32(-1), false)
	f.Add([]byte(strings.Repeat("é", DefaultMaxBytes/2+1)+"\nsecond\n"), uint16(1000), int32(1), uint8(0), int32(-1), false)
	// the whole-file path's ranges: negative and fractional offsets and limits, and the edge of the streamed range
	f.Add([]byte("a\nb\nc\nd"), uint16(2), int32(-3), uint8(0), int32(2), true)
	f.Add([]byte("a\nb\nc\nd"), uint16(2), int32(2), uint8(2), int32(1), true)
	f.Add([]byte("a\nb\nc\nd"), uint16(2), int32(1), uint8(0), int32(-1), true)
	f.Add([]byte("a\nb\nc\nd"), uint16(2), int32(1), uint8(1), int32(2), true)
	f.Add([]byte("a\nb\nc\nd"), uint16(2), int32(math.MaxInt32), uint8(0), int32(-1), false)
	f.Add([]byte("a\nb\nc\nd"), uint16(2), int32(1), uint8(0), int32(math.MaxInt32), true)
	f.Fuzz(func(t *testing.T, data []byte, chunk uint16, offset int32, offsetFraction uint8, limit int32, hasLimit bool) {
		var offsetArg, limitArg *float64
		if offset != 0 || offsetFraction%4 != 0 {
			offsetArg = new(float64(offset) + float64(offsetFraction%4)/4)
		}
		if hasLimit {
			limitArg = new(float64(limit))
		}
		size := int(chunk)%8192 + 1
		whole := readWith(t, wholeFileOps(data), offsetArg, limitArg)
		if got := readWith(t, streamingFallbackOps(data, size), offsetArg, limitArg); got != whole {
			t.Fatalf("chunks of %d, offset %v, limit %v:\n streamed %+v\n whole    %+v", size, offsetArg, limitArg, got, whole)
		}
		if isStreamableRange(readParams{Offset: offsetArg, Limit: limitArg}) {
			if want := referenceRead(decodeText(data), offsetArg, limitArg); whole != want {
				t.Fatalf("offset %v, limit %v:\n production %+v\n reference  %+v", offsetArg, limitArg, whole, want)
			}
		}
	})
}

// wholeFileOps reads the file with ReadFile alone, the path of an operations without CreateReadStream.
func wholeFileOps(data []byte) *ReadOperations {
	return &ReadOperations{
		ReadFile: func(string) ([]byte, error) { return data, nil },
		Access:   func(string) error { return nil },
	}
}

// streamingFallbackOps streams the file in chunks of size and also answers ReadFile, which a range that cannot stream calls.
func streamingFallbackOps(data []byte, size int) *ReadOperations {
	ops := wholeFileOps(data)
	ops.CreateReadStream = func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(&chunkReader{data: data, size: size}), nil
	}
	return ops
}
