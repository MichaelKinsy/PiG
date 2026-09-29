package pigsdk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"testing"
)

// referenceHash is the marker digest computed from whole-file reads, the form the staged markers on disk were written with.
func referenceHash(b sdkBundle) string {
	h := sha256.New()
	_, _ = h.Write([]byte("staging-v2\x00"))
	for _, name := range b.files {
		data, _ := fs.ReadFile(b.fsys, name)
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Streaming the embedded files must yield the digest of the whole-file form, or every staged SDK would restage.
func TestBundleHashStreamsTheWholeFileDigest(t *testing.T) {
	for _, b := range bundles() {
		got, err := b.computeHash()
		if err != nil || got != referenceHash(b) {
			t.Fatalf("%s %v %v", b.lang, got, err)
		}
	}
}

// BenchmarkComputeHashAll is the warm-start SDK marker check.
func BenchmarkComputeHashAll(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		for _, bundle := range bundles() {
			if _, err := bundle.computeHash(); err != nil {
				b.Fatal(err)
			}
		}
	}
}
