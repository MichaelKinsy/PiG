//go:build unix

package outputfiles

import (
	"syscall"
	"testing"
)

// A umask can only remove permission bits. With none, a file created with a looser mode would be world-readable.
func TestOutputFilesAreUserOnlyWithoutAUmask(t *testing.T) {
	useTempDir(t)
	previous := syscall.Umask(0)
	t.Cleanup(func() { syscall.Umask(previous) })
	path, err := WriteFile("pi-test", ".txt", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	requireUserOnly(t, path)
	streamPath, f, err := CreateStream("pi-test", ".txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	requireUserOnly(t, streamPath)
}
