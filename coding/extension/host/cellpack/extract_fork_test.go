//go:build unix

package cellpack

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The extracted cell binary is executed as soon as the Piglet Binary starts its extension. A fork while the staged copy is open leaves a child that holds the copy's write descriptor until it calls exec, and the host's exec of the cell then fails with "text file busy".
func TestAtomicWriteFileStartsNoChildWhileWriting(t *testing.T) {
	if testenv.RerunWithoutAsyncPreemption(t) {
		return
	}
	dest := filepath.Join(t.TempDir(), "runner")
	payload, err := testenv.CopyFromFIFO(t, func(fifo string) error {
		source, err := os.Open(fifo)
		if err != nil {
			return err
		}
		defer func() { _ = source.Close() }() // Read-only: close cannot lose data.
		return atomicWriteFile(dest, source, 0o755)
	}, func() {
		testenv.RequireForkLocked(t)
	})
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, payload) {
		t.Fatalf("file has %d bytes, want %d", len(written), len(payload))
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("file mode = %v, %v; want 0755", info, err)
	}
	if staged, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".extract-*")); len(staged) != 0 {
		t.Fatalf("staging left behind: %v", staged)
	}
}

// BenchmarkWriteCellBinary measures extracting one 16 MiB embedded cell binary that is not yet in place.
func BenchmarkWriteCellBinary(b *testing.B) {
	data := make([]byte, 16<<20)
	dir := b.TempDir()
	destination := filepath.Join(dir, "runner")
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if err := writeCellBinary(destination, data, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(destination); err != nil {
			b.Fatal(err)
		}
	}
}
