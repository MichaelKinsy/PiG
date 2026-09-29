package routing

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func unixSocketStaleError(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED) || errors.Is(err, windows.WSAECONNRESET) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}
