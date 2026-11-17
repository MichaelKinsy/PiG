//go:build darwin || dragonfly || freebsd || netbsd || openbsd

package tui

import "golang.org/x/sys/unix"

// The termios ioctls of the BSD family: TIOCGETA reads, TIOCSETAW sets after the output has drained (tcsetattr with TCSADRAIN).
const (
	ioctlReadTermios       = unix.TIOCGETA
	ioctlWriteTermiosDrain = unix.TIOCSETAW
)
