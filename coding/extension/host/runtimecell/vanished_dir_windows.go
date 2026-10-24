//go:build windows

package runtimecell

import (
	"errors"

	"golang.org/x/sys/windows"
)

// windowsDeletion classifies err by the codes Windows reports for a directory that another process is deleting.
func windowsDeletion(err error) (pending, denied bool) {
	return errors.Is(err, windows.ERROR_DELETE_PENDING), errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
