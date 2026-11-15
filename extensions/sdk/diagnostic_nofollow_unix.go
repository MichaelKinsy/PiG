//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package sdk

import "syscall"

// openNoFollow makes an open fail when the last element of the path is a symbolic link.
const openNoFollow = syscall.O_NOFOLLOW
