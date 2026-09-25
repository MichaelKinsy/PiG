//go:build windows

package testenv

import (
	"errors"

	"golang.org/x/sys/windows"
)

// symlinkPrivilegeMissing reports ERROR_PRIVILEGE_NOT_HELD, which Windows
// returns when SeCreateSymbolicLinkPrivilege is absent.
func symlinkPrivilegeMissing(err error) bool {
	return errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD)
}
