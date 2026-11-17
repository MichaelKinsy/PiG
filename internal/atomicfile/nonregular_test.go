//go:build !windows

package atomicfile

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A target that is not a regular file (a device such as /dev/null, a named pipe) is written in place, as Pi's writeFileSync writes it, and never replaced: a rename would put an ordinary file where the node was, with the node's mode, and no temporary file is made for it. A named pipe stands for the device node here, because a test cannot make a device and a process that can write /dev can; the bytes go to the pipe's reader and the node stays a pipe.
func TestWriteFileWritesANonRegularTargetInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node")
	if err := syscall.Mkfifo(path, 0o666); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	// The reader is open before the write, so that opening the pipe for writing does not wait for one.
	reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	reported := reportedPaths(t)

	const data = `{"provider-a":{"type":"api_key","key":"secret"}}`
	if err := WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Type() != os.ModeNamedPipe {
		t.Errorf("the node is now %v, want it to stay a named pipe", info.Mode().Type())
	}
	if got, err := io.ReadAll(reader); string(got) != data {
		t.Errorf("the pipe's reader got %q (%v), want the bytes written in place", got, err)
	}
	if len(*reported) != 1 || (*reported)[0] != path {
		t.Errorf("in-place writes reported = %v, want [%s]", *reported, path)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries after the write, want only the node", len(entries))
	}
}
