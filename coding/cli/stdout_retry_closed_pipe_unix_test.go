//go:build !windows

package cli

import (
	"errors"
	"io"
	"syscall"
	"testing"
)

func isClosedPipeWriteError(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}

func TestIsClosedPipeWriteError(t *testing.T) {
	if !isClosedPipeWriteError(syscall.EPIPE) {
		t.Fatal("expected EPIPE to be recognized as a closed-pipe write error")
	}
	if isClosedPipeWriteError(syscall.EAGAIN) {
		t.Fatal("expected EAGAIN not to be recognized as a closed-pipe write error")
	}
	if isClosedPipeWriteError(io.ErrUnexpectedEOF) {
		t.Fatal("expected ErrUnexpectedEOF not to be recognized as a closed-pipe write error")
	}
}
