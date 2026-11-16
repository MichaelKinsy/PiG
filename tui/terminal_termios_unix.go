//go:build unix

package tui

import (
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// libuvRawMode puts fd into the mode Node's process.stdin.setRawMode(true) gives it, which is the mode Pi's ProcessTerminal.start() runs in
// (packages/tui/src/terminal.ts): libuv's uv_tty_set_mode with UV_TTY_MODE_RAW (deps/uv/src/unix/tty.c, libuv 1.52.1, the one Node v24 carries). It
// is not cfmakeraw, which is what x/term's MakeRaw replicates and what libuv gives UV_TTY_MODE_IO: libuv's raw mode leaves the output processing
// the terminal had and adds ONLCR, so a program's LF still reaches the terminal as CR LF and Pi's `\r\n` as `\r\r\n`, and it touches only the input,
// control and local flags below. The state returned is the mode before, libuv's orig_termios, for the restore.
func libuvRawMode(fd int) (*term.State, error) {
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	termios, err := getTermios(fd)
	if err != nil {
		return nil, err
	}
	libuvRawTermios(termios)
	// "Apply changes after draining" (uv_tty_set_mode): tcsetattr(TCSADRAIN), which waits for the output already written to reach the terminal.
	return state, setTermiosAfterDraining(fd, termios)
}

// libuvRawTermios is the `case UV_TTY_MODE_RAW:` of uv_tty_set_mode, line for line:
//
//	tmp.c_iflag &= ~(BRKINT | ICRNL | INPCK | ISTRIP | IXON);
//	tmp.c_oflag |= (ONLCR);
//	tmp.c_cflag |= (CS8);
//	tmp.c_lflag &= ~(ECHO | ICANON | IEXTEN | ISIG);
//	tmp.c_cc[VMIN] = 1;
//	tmp.c_cc[VTIME] = 0;
func libuvRawTermios(termios *unix.Termios) {
	termios.Iflag &^= unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	termios.Oflag |= unix.ONLCR
	termios.Cflag |= unix.CS8
	termios.Lflag &^= unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	termios.Cc[unix.VMIN] = 1
	termios.Cc[unix.VTIME] = 0
}

// getTermios and setTermiosAfterDraining retry the ioctl on EINTR as uv__tcsetattr and the tcgetattr loop of uv_tty_set_mode do: a process that was
// stopped and continued resumes inside the call (see MakeRaw).
func getTermios(fd int) (*unix.Termios, error) {
	for {
		termios, err := unix.IoctlGetTermios(fd, ioctlReadTermios)
		if isEINTR(err) {
			continue
		}
		return termios, err
	}
}

func setTermiosAfterDraining(fd int, termios *unix.Termios) error {
	for {
		err := unix.IoctlSetTermios(fd, ioctlWriteTermiosDrain, termios)
		if isEINTR(err) {
			continue
		}
		return err
	}
}
