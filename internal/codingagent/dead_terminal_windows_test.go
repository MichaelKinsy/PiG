//go:build windows

package codingagent

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// Pi checks libuv's error.code, and libuv translates the Windows error numbers (internal/nodeerrno): ERROR_PIPE_NOT_CONNECTED and ERROR_BAD_PIPE
// are EPIPE, ERROR_GEN_FAILURE and ERROR_IO_DEVICE are EIO, ERROR_NOT_CONNECTED is ENOTCONN. Pi exits 129 for each.
func TestIsDeadTerminalErrorWindowsErrorNumbers(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"ERROR_PIPE_NOT_CONNECTED", &os.PathError{Op: "write", Path: "CONOUT$", Err: syscall.Errno(233)}, true},
		{"ERROR_BAD_PIPE", fmt.Errorf("write: %w", syscall.Errno(230)), true},
		{"ERROR_GEN_FAILURE", fmt.Errorf("write: %w", syscall.Errno(31)), true},
		{"ERROR_IO_DEVICE", fmt.Errorf("write: %w", syscall.Errno(1117)), true},
		{"ERROR_NOT_CONNECTED", fmt.Errorf("write: %w", syscall.Errno(2250)), true},
		{"ERROR_ACCESS_DENIED", fmt.Errorf("write: %w", syscall.Errno(5)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDeadTerminalError(tc.err); got != tc.want {
				t.Fatalf("isDeadTerminalError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
