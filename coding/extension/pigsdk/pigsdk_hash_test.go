package pigsdk

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sdkpack"
)

// referenceHash is the marker digest of the files of a bundle, fed one file at a time as "name\x00size\x00contents": the definition pigsdk has always named staged SDKs by, written out here independently of sdkpack.Hash.
func referenceHash(t *testing.T, b sdkBundle) string {
	t.Helper()
	blob, err := b.unpack()
	if err != nil {
		t.Fatalf("%s: %v", b.lang, err)
	}
	h := sha256.New()
	_, _ = h.Write([]byte("staging-v2\x00"))
	if err := sdkpack.Walk(blob, func(name string, data []byte) error {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", name, len(data))
		h.Write(data)
		return nil
	}); err != nil {
		t.Fatalf("%s: %v", b.lang, err)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// The hash a bundle carries, which names its staged tree and its marker, is the staging digest of the files its frame holds.
func TestBundleHashIsTheStagingDigestOfItsFiles(t *testing.T) {
	for _, b := range bundles() {
		if got := referenceHash(t, b); b.stagedHash != got {
			t.Errorf("%s bundle carries hash %s, its files hash to %s: run make sdk-bundles", b.lang, b.stagedHash, got)
		}
	}
}

// A start that stages nothing hashes and decodes nothing: it reads the hash from the bundle and the marker from disk, and the bundle's frame stays where the linker put it.
func TestWarmBundleCheckDecodesNothing(t *testing.T) {
	root := t.TempDir()
	for _, b := range bundles() {
		decoded := 0
		counting := b
		counting.unpack = func() ([]byte, error) {
			decoded++
			return b.unpack()
		}
		if changed, err := counting.ensure(root); err != nil || !changed || decoded != 1 {
			t.Fatalf("%s: first ensure staged = %v (%v), decoded %d times, want one stage and one decode", b.lang, changed, err, decoded)
		}
		if changed, err := counting.ensure(root); err != nil || changed || decoded != 1 {
			t.Fatalf("%s: second ensure staged = %v (%v), decoded %d times, want no stage and no decode", b.lang, changed, err, decoded)
		}
	}
}
