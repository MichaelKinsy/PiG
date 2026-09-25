//go:build !windows

package experimental

import (
	"errors"
	"syscall"
)

// connectionRefused reports a connect to a socket nothing listens on.
func connectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}
