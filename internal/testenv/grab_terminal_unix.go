//go:build unix

package testenv

import (
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
	if err := unix.IoctlSetPointerInt(int(tty.Fd()), unix.TIOCSPGRP, os.Getpid()); err != nil {
		fmt.Printf("tcsetpgrp: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(TookTheTerminal)
	os.Exit(0)
}
