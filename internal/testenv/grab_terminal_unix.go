//go:build unix

package testenv

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

// TerminalGrabberHelper is the environment variable that makes a test binary whose TestMain calls GrabTerminalIfRequested stand in for an interactive
// job-control shell such as `zsh -i`.
const TerminalGrabberHelper = "PIG_TEST_TERMINAL_GRABBER"

// Lines GrabTerminalIfRequested prints, so a test tells the two outcomes apart.
const (
	NoControllingTerminal = "no controlling terminal"
	TookTheTerminal       = "took the terminal"
)

// GrabTerminalIfRequested does what an interactive job-control shell does when it starts, and exits, when TerminalGrabberHelper is "1". The shell opens
// /dev/tty, leads a process group of its own, makes that group the terminal's foreground group (tcsetpgrp, with SIGTTOU ignored, as zsh does) and exits
// without giving the terminal back: it prints TookTheTerminal. A process with no controlling terminal cannot open /dev/tty: it prints
// NoControllingTerminal. A TestMain calls it first, so the test binary can stand in for the shell.
func GrabTerminalIfRequested() {
	if os.Getenv(TerminalGrabberHelper) != "1" {
		return
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		fmt.Printf("%s: %v\n", NoControllingTerminal, err)
		os.Exit(0)
	}
	if err := syscall.Setpgid(0, 0); err != nil {
		fmt.Printf("setpgid: %v\n", err)
		os.Exit(1)
	}
	signal.Ignore(syscall.SIGTTOU)
	if err := ioctlSetPointerInt(int(tty.Fd()), unix.TIOCSPGRP, os.Getpid()); err != nil {
		fmt.Printf("tcsetpgrp: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(TookTheTerminal)
	os.Exit(0)
}

// isEINTR reports whether a terminal ioctl error is an interrupted syscall worth retrying. Go's runtime signals threads for goroutine preemption, so
// a terminal ioctl that sleeps in the kernel can return EINTR in a test helper as it can in the terminal code (tui.isEINTR).
func isEINTR(err error) bool { return errors.Is(err, unix.EINTR) }

// ioctlGetInt is unix.IoctlGetInt that goes round again on EINTR. The requests the helpers make (TIOCGPGRP, TIOCGPTN) are idempotent queries.
func ioctlGetInt(fd int, req uint) (int, error) {
	for {
		value, err := unix.IoctlGetInt(fd, req)
		if isEINTR(err) {
			continue
		}
		return value, err
	}
}

// ioctlSetPointerInt is unix.IoctlSetPointerInt that goes round again on EINTR. The requests the helpers make (TIOCSPGRP, TIOCSPTLCK) set a
// value outright, so a repeat after an interruption that did not apply it, or that applied it, leaves the same state.
func ioctlSetPointerInt(fd int, req uint, value int) error {
	for {
		err := unix.IoctlSetPointerInt(fd, req, value)
		if isEINTR(err) {
			continue
		}
		return err
	}
}
