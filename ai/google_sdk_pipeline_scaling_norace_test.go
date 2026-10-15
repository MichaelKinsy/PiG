//go:build !race

package ai

import (
	"strings"
	"testing"
)

// googleLongRecordBytes is what processStreamResponse allocates for one record of about size bytes that arrives in 1 KB reads.
func googleLongRecordBytes(t *testing.T, size int) uint64 {
	body := []byte(`data: {"candidates":[{"content":{"parts":[{"text":"` + strings.Repeat("x", size) + `"}]}}]}` + "\r\n\r\n")
	var chunks [][]byte
	for rest := body; len(rest) > 0; {
		n := min(1024, len(rest))
		chunks = append(chunks, rest[:n])
		rest = rest[n:]
	}
	return allocatedBytes(func() {
		records, err := googleGeneratorRecords(chunks)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 1 || len(records[0]) < size {
			t.Fatalf("read %d records, want 1 of over %d bytes", len(records), size)
		}
	})
}

// A record that spans many reads costs memory in proportion to its size: the generator appends each read to its buffer and searches only the bytes that can still start a delimiter, instead of concatenating the whole buffer and searching it from the start on every read.
func TestGoogleSDKLongRecordIsLinear(t *testing.T) {
	small, large := googleLongRecordBytes(t, 64<<10), googleLongRecordBytes(t, 640<<10)
	ratio := float64(large) / float64(small)
	t.Logf("64 KB record: %d B allocated; 640 KB record: %d B (%.1fx)", small, large, ratio)
	if ratio > 20 {
		t.Fatalf("a 10x longer record allocated %.1fx the bytes (%d vs %d); want at most 20x", ratio, large, small)
	}
}
