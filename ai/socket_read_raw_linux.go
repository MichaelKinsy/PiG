//go:build linux && !race

package ai

import (
	"syscall"
	"unsafe"
)

// readSocketOnce issues one read(2) of fd into buffer as a raw system call, which neither enters nor leaves a blocking system call.
func readSocketOnce(fd uintptr, buffer []byte) (uintptr, syscall.Errno) {
	//nolint:gosec // G103: read(2) into the caller's non-empty buffer; RawSyscall keeps the pointer alive for the call (go:uintptrkeepalive).
	n, _, errno := syscall.RawSyscall(syscall.SYS_READ, fd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	return n, errno
}
