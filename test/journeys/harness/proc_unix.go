//go:build unix

package harness

import "syscall"

func signalPID(pid, sig int) error { return syscall.Kill(pid, syscall.Signal(sig)) }
