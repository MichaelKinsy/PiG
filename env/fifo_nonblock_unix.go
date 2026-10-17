//go:build unix

package env

import "syscall"

// nonBlock opens a FIFO without waiting for its other end (O_NONBLOCK).
const nonBlock = syscall.O_NONBLOCK
