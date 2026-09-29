//go:build windows

package tui

import (
	"os"
	"reflect"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
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
	var returned uint32
	if err := windows.DeviceIoControl(windows.Handle(file.Fd()), windows.FSCTL_SET_SPARSE, nil, 0, nil, 0, &returned, nil); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(int64(size)); err != nil {
		t.Fatal(err)
	}
	mapping, err := windows.CreateFileMapping(windows.Handle(file.Fd()), nil, windows.PAGE_READWRITE, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.CloseHandle(mapping); err != nil {
			t.Error(err)
		}
	})
	address, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ|windows.FILE_MAP_WRITE, 0, 0, uintptr(size))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := windows.UnmapViewOfFile(address); err != nil {
			t.Error(err)
		}
	})
	var data []byte
	// The address comes from MapViewOfFile, not the Go heap. Mutating an existing slice header keeps the foreign view explicit without an unchecked uintptr-to-Go-pointer conversion.
	header := (*reflect.SliceHeader)(unsafe.Pointer(&data)) //nolint:staticcheck,gosec // Foreign mapped memory owns these bytes; allocating a replacement Go slice would allocate the upstream billion-entry fixture densely.
	header.Data, header.Len, header.Cap = address, size, size
	return data
}
