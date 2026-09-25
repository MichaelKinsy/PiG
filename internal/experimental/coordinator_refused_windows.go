//go:build windows

package experimental

import (
	"errors"

	"golang.org/x/sys/windows"
)

// connectionRefused reports a connect to a socket nothing listens on. Winsock
// reports WSAECONNREFUSED, which syscall.ECONNREFUSED does not match on
// Windows.
func connectionRefused(err error) bool {
	return errors.Is(err, windows.WSAECONNREFUSED)
}
