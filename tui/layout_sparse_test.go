package tui

import (
	"testing"
	"unsafe"
)

// sparseLayoutLines represents the upstream billion-length sparse JS array without a billion-entry Go heap allocation. The file-backed mapping contains zero string headers for holes; tests populate only static string literals, so no Go heap pointer is hidden from the collector. The test owns and unmaps the view at cleanup.
func sparseLayoutLines(t *testing.T, length int) []string {
	t.Helper()
	if length == 0 {
		return []string{}
	}
	entrySize := int(unsafe.Sizeof(""))
	if length < 0 || length > int(^uint(0)>>1)/entrySize {
		t.Fatal("sparse fixture exceeds process address space")
	}
	memory := mapSparseLayoutMemory(t, length*entrySize)
	return unsafe.Slice((*string)(unsafe.Pointer(&memory[0])), length) //nolint:gosec // Audited test-only sparse mapping: headers point only to static fixture strings; a Go allocation would require 16 GB of heap.
}
