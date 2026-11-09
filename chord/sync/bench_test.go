package sync

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"testing"
)

// BenchmarkDecodeBatchLinesOneLongLine shows the decoder's cost is linear in the length of a line that never ends:
// the time per byte (ns/op divided by the size) stays flat from 4 to 32 MiB, as it did not when every chunk rescanned
// the whole unfinished line for a newline.
func BenchmarkDecodeBatchLinesOneLongLine(b *testing.B) {
	for _, mib := range []int{4, 8, 16, 32} {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		chunk := bytes.Repeat([]byte("x"), 64*1024)
		for range mib * 16 {
			if _, err := writer.Write(chunk); err != nil {
				b.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("%dMiB", mib), func(b *testing.B) {
			b.SetBytes(int64(mib) << 20)
			for range b.N {
				lines := DecodeBatchLines(chunked(compressed.Bytes(), 64*1024))
				if _, _, err := lines.Next(background); err == nil {
					b.Fatal("a body that ends inside a line decoded")
				}
			}
		})
	}
}
