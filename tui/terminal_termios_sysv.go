//go:build aix || linux || solaris || zos

package tui

import "golang.org/x/sys/unix"

// The termios ioctls of the System V family: TCGETS reads, TCSETSW sets after the output has drained (tcsetattr with TCSADRAIN).
const (
	ioctlReadTermios       = unix.TCGETS
	ioctlWriteTermiosDrain = unix.TCSETSW
)
