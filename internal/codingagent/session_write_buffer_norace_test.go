//go:build !race

package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// newSessionFileBytesCeiling bounds the heap bytes of writing a new session file's first records (a header and a message, about
// 500 bytes), the write a print run makes when its first message persists. It measured 584 bytes on the Mac: the file, its path and
// a buffer the size of the records. A fixed 64 KB write buffer made it 65,768.
const newSessionFileBytesCeiling = 2_048

func TestNewSessionFileWriteSizesItsBuffer(t *testing.T) {
	dir := t.TempDir()
	header := []byte(`{"type":"session","version":3,"id":"0199e0a0-0000-7000-8000-000000000000","timestamp":"2026-10-14T00:00:00.000Z","cwd":"/tmp/pig-startup/w"}`)
	message := []byte(`{"type":"message","id":"a1b2c3d4","parentId":null,"timestamp":"2026-10-14T00:00:00.001Z","message":{"role":"user","content":[{"type":"text","text":"reply with exactly: ok"}],"timestamp":1791936000001}}`)
	lines := [][]byte{header, message}
	paths := make([]string, 21)
	for i := range paths {
		paths[i] = filepath.Join(dir, "s"+string(rune('a'+i))+".jsonl")
	}
	if err := writeSessionFile(paths[0], lines, os.O_TRUNC); err != nil {
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	for _, path := range paths[1:] {
		if err := writeSessionFile(path, lines, os.O_TRUNC); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	perWrite := (after.TotalAlloc - before.TotalAlloc) / uint64(len(paths)-1)
	t.Logf("a new session file's first records allocate %d bytes", perWrite)
	if perWrite > newSessionFileBytesCeiling {
		t.Errorf("a new session file's first records allocate %d bytes, ceiling %d", perWrite, newSessionFileBytesCeiling)
	}
	data, err := os.ReadFile(paths[len(paths)-1])
	if err != nil {
		t.Fatal(err)
	}
	if want := string(header) + "\n" + string(message) + "\n"; string(data) != want {
		t.Errorf("session file = %q, want %q", data, want)
	}
}
