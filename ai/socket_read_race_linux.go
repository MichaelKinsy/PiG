//go:build linux && race

package ai

import "syscall"

// readSocketOnce issues one read(2) of fd into buffer through syscall.Read. Under the race detector, syscall.Read records that the read happens after every earlier file write in the process, as net.Conn.Read does; a raw system call records nothing, so the detector would report data a peer goroutine wrote before sending as racing with the reader that received it.
func readSocketOnce(fd uintptr, buffer []byte) (uintptr, syscall.Errno) {
	n, err := syscall.Read(int(fd), buffer)
	if err == nil {
		return uintptr(n), 0
	}
	// syscall.Read reports every failure as a syscall.Errno.
	errno, _ := err.(syscall.Errno)
	return 0, errno
}
