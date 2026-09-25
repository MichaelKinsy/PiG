//go:build windows

package testenv

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// Symlink skips only when Windows refuses the symlink privilege. Any other
// failure, such as a denied or missing directory, still fails the test.
func TestSymlinkPrivilegeMissingMatchesOnlyThePrivilegeError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"privilege not held", &os.LinkError{Op: "symlink", Old: "a", New: "b", Err: windows.ERROR_PRIVILEGE_NOT_HELD}, true},
		{"access denied", &os.LinkError{Op: "symlink", Old: "a", New: "b", Err: windows.ERROR_ACCESS_DENIED}, false},
		{"path not found", &os.LinkError{Op: "symlink", Old: "a", New: "b", Err: windows.ERROR_PATH_NOT_FOUND}, false},
		{"other error", errors.New("symlink failed"), false},
	} {
		if got := symlinkPrivilegeMissing(tc.err); got != tc.want {
			t.Errorf("%s: symlinkPrivilegeMissing = %v, want %v", tc.name, got, tc.want)
		}
	}
}
