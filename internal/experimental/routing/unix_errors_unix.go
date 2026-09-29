//go:build !windows

package routing

import (
	"errors"
	"os"
	"syscall"
)

func unixSocketStaleError(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET)
}
