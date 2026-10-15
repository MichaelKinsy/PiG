//go:build !race

package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The JSONL export writes through the session file's buffer, sized for the lines up to sessionWriteBuffer, so a small export allocates
// about its size, as TestNewSessionFileWriteSizesItsBuffer requires of writeSessionFile.
func TestASmallExportAllocatesAboutItsSize(t *testing.T) {
	dir := t.TempDir()
	lines := [][]byte{bytes.Repeat([]byte("h"), 300), bytes.Repeat([]byte("u"), 400), bytes.Repeat([]byte("a"), 500)}
	size := 0
	for _, line := range lines {
		size += len(line) + 1
	}
	for name, write := range map[string]func(string) error{
		"export": func(path string) error { return writeLines(path, lines) },
	} {
		path := filepath.Join(dir, name+".jsonl")
		var err error
		allocated := lwAllocated(func() { err = write(path) })
		if err != nil {
			t.Fatal(err)
		}
		if written, err := os.ReadFile(path); err != nil || len(written) != size {
			t.Fatalf("%s: wrote %d bytes (%v), want %d", name, len(written), err, size)
		}
		// Opening the file costs a few hundred bytes; a buffer of the lines' size is the rest.
		if limit := uint64(size + 2048); allocated > limit {
			t.Fatalf("%s: writing %d bytes allocated %d B, want at most %d: the buffer is not sized for the lines", name, size, allocated, limit)
		}
	}
}
