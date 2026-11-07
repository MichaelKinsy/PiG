//go:build unix

package testenv

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// ReportSessionIfRequested writes the process's session ID to stdout and exits when SessionHelper is "1". A TestMain calls it
// first, so the test binary can stand in for the one portable way to read a session ID: ps has no session keyword that macOS and
// Linux share (`ps -o sid` is Linux's; macOS has no such keyword).
func ReportSessionIfRequested() {
	if os.Getenv(SessionHelper) != "1" {
		return
	}
	sid, err := unix.Getsid(0)
	if err != nil {
		os.Exit(1)
	}
	if _, err := os.Stdout.WriteString(strconv.Itoa(sid)); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
