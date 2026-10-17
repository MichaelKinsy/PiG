//go:build !linux && !windows

package nodespawn

import "syscall"

// miscErrno reports false: only Linux applies the kernel's binfmt_misc rules.
func miscErrno(string, string, []byte, int, bool) (syscall.Errno, bool) { return 0, false }
