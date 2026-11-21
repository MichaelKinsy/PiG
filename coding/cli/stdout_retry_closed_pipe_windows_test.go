//go:build windows

package cli

import (
	"errors"
	"io"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func isClosedPipeWriteError(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_BROKEN_PIPE)
}

func TestIsClosedPipeWriteError(t *testing.T) {
	for _, err := range []error{syscall.EPIPE, windows.ERROR_NO_DATA, windows.ERROR_BROKEN_PIPE} {
		if !isClosedPipeWriteError(err) {
			t.Fatalf("expected %v to be recognized as a closed-pipe write error", err)
		}
	}
	if isClosedPipeWriteError(syscall.EAGAIN) {
		t.Fatal("expected EAGAIN not to be recognized as a closed-pipe write error")
	}
	if isClosedPipeWriteError(io.ErrUnexpectedEOF) {
		t.Fatal("expected ErrUnexpectedEOF not to be recognized as a closed-pipe write error")
	}
}
