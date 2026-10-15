//go:build windows

package host

import (
	"testing"

	"golang.org/x/sys/windows"
)

// The IPC pipe pair must be connectable at once, every time: the parent's end is dialled right after the child's
// server instance is created, with no instance-creating goroutine to race (ERROR_PIPE_BUSY).
func TestNewIPCPipeAlwaysConnects(t *testing.T) {
	for i := 0; i < 500; i++ {
		conn, child, err := newIPCPipe()
		if err != nil {
			t.Fatalf("pair %d: %v", i, err)
		}
		_ = conn.Close()
		_ = windows.CloseHandle(child)
	}
}
