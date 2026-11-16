//go:build unix && !linux && !darwin && !dragonfly && !freebsd && !netbsd && !openbsd

package workers

import "syscall"

// streamHolds is -1 here (cannot say): no platform this module ships for is one of these Unix systems (AIX, illumos,
// Solaris), whose request x/sys does not name either. The drain then settles by a read that finds the stream empty.
func streamHolds(syscall.RawConn) int { return -1 }
