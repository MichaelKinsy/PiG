//go:build windows

package ai

import "golang.org/x/sys/windows"

// What this system gives a reset socket and a socket shut down for sending, and package net's name for a vectored write.
const (
	socketResetErrno = windows.WSAECONNRESET
	socketPipeErrno  = windows.WSAESHUTDOWN
	vectoredWriteOp  = "wsasend"
)
