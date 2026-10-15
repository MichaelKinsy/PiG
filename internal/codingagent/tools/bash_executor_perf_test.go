package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type repeatedChunkOperations struct {
	chunks  int
	chunk   []byte
	onChunk func(i int)
}

func (o repeatedChunkOperations) Exec(_ context.Context, _, _ string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	buf := make([]byte, len(o.chunk))
	for i := range o.chunks {
		copy(buf, o.chunk)
		opts.OnData(buf)
		if o.onChunk != nil {
			o.onChunk(i)
		}
	}
	code := 0
	return BashOperationsResult{ExitCode: &code}, nil
}

// The user-bash executor's chunk path (decode, ANSI strip, sanitize, CR strip, the rolling window) costs at most two allocations per clean chunk: the kept
// text and its slot in the window.
func TestUserBashAllocsPerChunk(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	const chunks = 2000
	ops := repeatedChunkOperations{chunks: chunks, chunk: []byte(strings.Repeat("x", 99) + "\n")}
	allocs := testing.AllocsPerRun(3, func() {
		if _, err := ExecuteBashWithOperations(context.Background(), "", "", ops, BashExecOptions{OnChunk: func(string) {}}); err != nil {
			t.Fatal(err)
		}
	})
	if perChunk := allocs / chunks; perChunk > 2 {
		t.Fatalf("%.2f allocations per chunk, want at most 2", perChunk)
	}
}

// Past the byte limit user bash streams its sanitized output to the full-output file. The writes are buffered: the file grows in large steps, not by a
// write(2) per chunk. The file still holds the whole output once the command returns.
func TestUserBashSpillCoalescesWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	const chunks = 3000
	chunk := strings.Repeat("y", 63) + "\n"
	growths, lastSize := 0, int64(0)
	ops := repeatedChunkOperations{chunks: chunks, chunk: []byte(chunk), onChunk: func(int) {
		matches, _ := filepath.Glob(filepath.Join(dir, "pi-bash-*.log"))
		if len(matches) != 1 {
			return
		}
		info, err := os.Stat(matches[0])
		if err == nil && info.Size() != lastSize {
			growths++
			lastSize = info.Size()
		}
	}}
	res, err := ExecuteBashWithOperations(context.Background(), "", "", ops, BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if growths > chunks/16 {
		t.Errorf("the full-output file grew %d times over %d chunks: a write per chunk", growths, chunks)
	}
	got, err := os.ReadFile(res.FullOutputPath)
	if err != nil || string(got) != strings.Repeat(chunk, chunks) {
		t.Fatalf("full-output file holds %d bytes (err %v), want %d", len(got), err, chunks*len(chunk))
	}
}
