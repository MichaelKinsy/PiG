//go:build unix

package tui

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func mapSparseLayoutMemory(t *testing.T, size int) []byte {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "sparse-lines-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := file.Truncate(int64(size)); err != nil {
		t.Fatal(err)
	}
	data, err := unix.Mmap(int(file.Fd()), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unix.Munmap(data); err != nil {
			t.Error(err)
		}
	})
	return data
}
