//go:build unix

package ai

import "syscall"

// What this system gives a reset socket and a socket shut down for sending, and package net's name for a vectored write.
const (
	socketResetErrno = syscall.ECONNRESET
	socketPipeErrno  = syscall.EPIPE
	vectoredWriteOp  = "writev"
)
