//go:build unix

package tui

import (
	"errors"

	"golang.org/x/sys/unix"
)

// rawState is the terminal mode saved before raw mode, to hand back by restoreRaw.
type rawState = unix.Termios

func isEINTR(err error) bool { return errors.Is(err, unix.EINTR) }

// makeRaw puts fd into raw mode (the settings of x/term.MakeRaw) and returns the state to restore. A stopped and continued process
// resumes inside the termios ioctl, which macOS reports as EINTR; Node's setRawMode retries it (libuv uv_tty_set_mode), so the
// ioctls here retry too. Chord cannot use tui.MakeRaw, which is a Pi package outside its boundary.
func makeRaw(fd int) (*rawState, error) {
	termios, err := getTermios(fd)
	if err != nil {
		return nil, err
	}
	saved := *termios
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	termios.Oflag &^= unix.OPOST
	termios.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	termios.Cflag &^= unix.CSIZE | unix.PARENB
	termios.Cflag |= unix.CS8
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
	if err := setTermios(fd, termios); err != nil {
		return nil, err
	}
	return &saved, nil
}

// restoreRaw hands fd back in the mode state captured, retrying EINTR as makeRaw does.
func restoreRaw(fd int, state *rawState) error { return setTermios(fd, state) }

func getTermios(fd int) (*unix.Termios, error) {
	for {
		termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
		if isEINTR(err) {
			continue
		}
		return termios, err
	}
}

func setTermios(fd int, termios *unix.Termios) error {
	for {
		err := unix.IoctlSetTermios(fd, ioctlWriteTermios, termios)
		if isEINTR(err) {
			continue
		}
		return err
	}
}
