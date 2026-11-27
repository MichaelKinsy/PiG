// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"errors"
	"os"
	"syscall"
)

func flockExclusive(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func flockTryExclusive(f *os.File) (bool, error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err == nil {
			return true, nil
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return false, nil
		}
		return false, err
	}
}

func flockShared(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func flockUnlock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// lockLeaseFile acquires an exclusive process-lifetime lock on f.
func lockLeaseFile(f *os.File) error {
	return flockExclusive(f)
}

// tryLockLeaseFile probes whether any live process holds the lease lock on f.
func tryLockLeaseFile(f *os.File) (bool, error) {
	return flockTryExclusive(f)
}

// unlockLeaseFile releases the lock acquired by lockLeaseFile/tryLockLeaseFile.
func unlockLeaseFile(f *os.File) error {
	return flockUnlock(f)
}
