//go:build !windows

package env

import (
	"os"
	"syscall"
)

// redeliverFIFOEOF opens and closes a write end of the FIFO at path without blocking, which is what a writer that
// closes does for a reader: the last close of a write end signals end of file. With no reader open the open fails
// with ENXIO and there is nothing to signal.
func redeliverFIFOEOF(path string) {
	file, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return
	}
	_ = file.Close()
}
