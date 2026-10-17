package pigletbuild

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// hashFile digests every Piglet Binary and release asset (70 MB and more each),
// several times per build and verify. It streams the file: the heap it allocates
// stays bounded by a copy buffer, not by the artifact size.
func TestHashFileStreamsTheFile(t *testing.T) {
	const size = 32 << 20
	path := filepath.Join(t.TempDir(), "artifact")
	want := writeArtifact(t, path, size)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	digest, got, err := hashFile(path)
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if digest != want || got != size {
		t.Fatalf("hashFile = %s, %d; want %s, %d", digest, got, want, size)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/8 {
		t.Errorf("hashFile allocated %d bytes for a %d-byte file; it must stream", allocated, size)
	}
}

func TestHashFileReportsAMissingFile(t *testing.T) {
	if _, _, err := hashFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("a missing file must be an error")
	}
}

// writeArtifact writes size bytes to path and returns their digest, keeping none of them alive.
func writeArtifact(t *testing.T, path string, size int) string {
	t.Helper()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i * 31)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
