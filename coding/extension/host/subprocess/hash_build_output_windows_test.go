//go:build windows

package subprocess

import (
	"errors"

	"golang.org/x/sys/windows"
)

// churnRemovalBusy reports whether err is Windows refusing to delete an entry the hash walk holds open, or one already pending deletion.
func churnRemovalBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_DELETE_PENDING)
}
