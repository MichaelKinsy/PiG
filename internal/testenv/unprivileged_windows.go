//go:build windows

package testenv

import (
	"testing"

	"golang.org/x/sys/windows"
)

// runUnprivileged skips the test when the process token is elevated: an administrator bypasses the read-only ACLs the permission fixtures set, so the test cannot observe the denial it asserts. Otherwise the test continues in this process.
func runUnprivileged(t *testing.T) bool {
	t.Helper()
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("requires an unprivileged process: an elevated administrator bypasses read-only ACLs")
	}
	return false
}
