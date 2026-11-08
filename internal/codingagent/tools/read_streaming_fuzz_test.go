package tools

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzReadStreamingMatchesTheWholeFileAlgorithm reads arbitrary bytes, chunk size, offset and limit through the streamed path and
// through the whole-file reference: lines, truncation, counts and the decoding of invalid UTF-8 split at chunk ends must agree.
func FuzzReadStreamingMatchesTheWholeFileAlgorithm(f *testing.F) {
	many := repeatLines(3000, func(i int) string { return fmt.Sprintf("line %d é€", i) })
	f.Add([]byte("alpha\nbeta\ngamma"), uint16(1), uint16(0), int32(-1))
	f.Add([]byte("a\n\n\nb\n"), uint16(3), uint16(2), int32(1))
	f.Add([]byte("a\xc3\n\xe2\x82\n\xff\xfe\x80\xf0\x9f\x98\n\xed\xa0\x80"), uint16(2), uint16(0), int32(-1))
	f.Add([]byte(many), uint16(4096), uint16(0), int32(-1))
	f.Add([]byte(many), uint16(777), uint16(2500), int32(10))
	f.Add([]byte(many+"\n\n"), uint16(64), uint16(1), int32(2999))
	f.Add([]byte(many+"\xe2\x82"), uint16(5000), uint16(3), int32(2990))
	f.Add([]byte("a\nb\n"+strings.Repeat("z", 60*1024)+"\nc"), uint16(4096), uint16(0), int32(-1))
	f.Add([]byte(strings.Repeat("é", DefaultMaxBytes/2+1)+"\nsecond\n"), uint16(1000), uint16(1), int32(-1))
	f.Fuzz(func(t *testing.T, data []byte, chunk uint16, offset uint16, limit int32) {
		var offsetArg, limitArg *float64
		if offset != 0 {
			offsetArg = new(float64(offset))
		}
		if limit >= 0 {
			limitArg = new(float64(limit))
		}
		want := referenceRead(decodeText(data), offsetArg, limitArg)
		size := int(chunk)%8192 + 1
		if got := readWith(t, streamingOps(data, size), offsetArg, limitArg); got != want {
			t.Fatalf("chunks of %d, offset %v, limit %v:\n got  %+v\n want %+v", size, offset, limit, got, want)
		}
	})
}
