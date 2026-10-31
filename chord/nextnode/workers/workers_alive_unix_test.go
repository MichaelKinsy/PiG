//go:build unix

package workers

import "syscall"

// alive reports whether process pid lives.
func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }
