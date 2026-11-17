//go:build !linux && !darwin && !windows

package main

import "errors"

// processStart cannot tell on this system: a replay directory of an owner that cannot be looked up is removed by its age alone
// (replayStaleAfter).
func processStart(int) (string, error) {
	return "", errors.New("process start time is not available on this system")
}

func processScope() string { return "" }
