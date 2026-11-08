//go:build unix

package host

import (
	"os"
	"os/signal"
	"syscall"
)

// openNoFollow opens path read-only without following a final symlink and without blocking on a FIFO.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

// createExclusive creates a file that must not exist yet, without following a final symlink.
func createExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
}

// suspendThisProcess is Ctrl+Z: it ignores SIGINT while the process group is stopped and returns on SIGCONT.
func suspendThisProcess() error {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	resumed := make(chan os.Signal, 1)
	signal.Notify(resumed, syscall.SIGCONT)
	defer signal.Stop(resumed)
	if err := syscall.Kill(0, syscall.SIGTSTP); err != nil {
		return err
	}
	<-resumed
	return nil
}
