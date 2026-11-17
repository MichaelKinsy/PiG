//go:build windows

package main

import (
	"errors"
	"strconv"
	"syscall"
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	errorInvalidParameter          = syscall.Errno(87) // ERROR_INVALID_PARAMETER: what OpenProcess says of a pid no process has
)

// processStart is the creation time of process pid (a FILETIME). A process that has exited is gone, though its handle may live on.
func processStart(pid int) (string, error) {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		if errors.Is(err, errorInvalidParameter) {
			return "", errProcessGone
		}
		return "", err
	}
	defer func() { _ = syscall.CloseHandle(h) }()
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return "", err
	}
	if code != stillActive {
		return "", errProcessGone
	}
	var created, exited, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return "", err
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), nil
}

// processScope: a pid means the same process to every user of the machine.
func processScope() string { return "" }
