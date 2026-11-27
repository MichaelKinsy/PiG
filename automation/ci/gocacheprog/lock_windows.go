// SPDX-License-Identifier: MIT

//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	lockfileFailImmediately = 0x00000001
	lockfileExclusiveLock   = 0x00000002
	errLockViolation        = syscall.Errno(33)

	// Lease files lock a high byte offset outside the file's data range so Windows
	// mandatory byte-range locking does not block peer pruners from reading the
	// pinned paths recorded at offset 0.
	leaseLockOffsetHigh = 0x7fffffff
	leaseLockOffsetLow  = 0
	leaseLockLenLow     = 1
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

func lockFileRangeEx(f *os.File, flags uint32, offLow, offHigh, lenLow, lenHigh uint32) error {
	ol := syscall.Overlapped{
		Offset:     offLow,
		OffsetHigh: offHigh,
	}
	r1, _, err := procLockFileEx.Call(
		f.Fd(),
		uintptr(flags),
		0,
		uintptr(lenLow),
		uintptr(lenHigh),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return err
	}
	return nil
}

func unlockFileRangeEx(f *os.File, offLow, offHigh, lenLow, lenHigh uint32) error {
	ol := syscall.Overlapped{
		Offset:     offLow,
		OffsetHigh: offHigh,
	}
	r1, _, err := procUnlockFileEx.Call(
		f.Fd(),
		0,
		uintptr(lenLow),
		uintptr(lenHigh),
		uintptr(unsafe.Pointer(&ol)),
	)
	if r1 == 0 {
		return err
	}
	return nil
}

func flockExclusive(f *os.File) error {
	return lockFileRangeEx(f, lockfileExclusiveLock, 0, 0, ^uint32(0), ^uint32(0))
}

func flockTryExclusive(f *os.File) (bool, error) {
	err := lockFileRangeEx(f, lockfileExclusiveLock|lockfileFailImmediately, 0, 0, ^uint32(0), ^uint32(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errLockViolation) {
		return false, nil
	}
	return false, err
}

func flockShared(f *os.File) error {
	return lockFileRangeEx(f, 0, 0, 0, ^uint32(0), ^uint32(0))
}

func flockUnlock(f *os.File) error {
	return unlockFileRangeEx(f, 0, 0, ^uint32(0), ^uint32(0))
}

// lockLeaseFile locks a sentinel byte past the data region so peer pruners on
// Windows can still read pinned paths from offset 0 while the lease is live.
func lockLeaseFile(f *os.File) error {
	return lockFileRangeEx(f, lockfileExclusiveLock, leaseLockOffsetLow, leaseLockOffsetHigh, leaseLockLenLow, 0)
}

func tryLockLeaseFile(f *os.File) (bool, error) {
	err := lockFileRangeEx(f, lockfileExclusiveLock|lockfileFailImmediately, leaseLockOffsetLow, leaseLockOffsetHigh, leaseLockLenLow, 0)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errLockViolation) {
		return false, nil
	}
	return false, err
}

func unlockLeaseFile(f *os.File) error {
	return unlockFileRangeEx(f, leaseLockOffsetLow, leaseLockOffsetHigh, leaseLockLenLow, 0)
}
