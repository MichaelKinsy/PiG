// SPDX-License-Identifier: MIT

//go:build !unix && !windows

package main

import (
	"errors"
	"os"
)

var errLockUnsupported = errors.New("gocacheprog: file locking not supported on this platform")

func flockExclusive(_ *os.File) error {
	return errLockUnsupported
}

func flockTryExclusive(_ *os.File) (bool, error) {
	return false, errLockUnsupported
}

func flockShared(_ *os.File) error {
	return errLockUnsupported
}

func flockUnlock(_ *os.File) error {
	return errLockUnsupported
}

func lockLeaseFile(_ *os.File) error {
	return errLockUnsupported
}

func tryLockLeaseFile(_ *os.File) (bool, error) {
	return false, errLockUnsupported
}

func unlockLeaseFile(_ *os.File) error {
	return errLockUnsupported
}
