//go:build unix

package subprocess

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The binary cargo built is copied to the cache path and executed as soon as the build is published. A fork while the copy is open leaves a child that holds the copy's write descriptor until it calls exec, and the host's exec then fails with "text file busy".
func TestCopyBuiltBinaryStartsNoChildWhileCopying(t *testing.T) {
	if testenv.RerunWithoutAsyncPreemption(t) {
		return
	}
	dst := filepath.Join(t.TempDir(), "bin.tmp")
	payload, err := testenv.CopyFromFIFO(t, func(fifo string) error {
		return copyBuiltBinary(fifo, dst)
	}, func() {
		testenv.RequireForkLocked(t)
	})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copied, payload) {
		t.Fatalf("copy has %d bytes, want the %d cargo wrote", len(copied), len(payload))
	}
	if info, err := os.Stat(dst); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("copy is not executable: %v, %v", info, err)
	}
}

// BenchmarkCopyBuiltBinary measures copying a 16 MiB cargo output to the cache path.
func BenchmarkCopyBuiltBinary(b *testing.B) {
	source := filepath.Join(b.TempDir(), "built")
	if err := os.WriteFile(source, make([]byte, 16<<20), 0o755); err != nil {
		b.Fatal(err)
	}
	destination := filepath.Join(b.TempDir(), "bin.tmp")
	b.ReportAllocs()
	b.SetBytes(16 << 20)
	for b.Loop() {
		if err := copyBuiltBinary(source, destination); err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(destination); err != nil {
			b.Fatal(err)
		}
	}
}
