//go:build !windows

package pilock

import "syscall"

var nonEEXISTMkdirErrors = map[string]error{
	"EPERM":  syscall.EPERM,
	"EACCES": syscall.EACCES,
	"EBUSY":  syscall.EBUSY,
}
