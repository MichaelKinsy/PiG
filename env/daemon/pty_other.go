//go:build !linux && !darwin

package daemon

// Ports packages/env/daemon/src/pty.rs

// Terminals are not supported on this system yet: `pty` answers ENOTSUP, and no terminal ever exists for the other
// operations to reach.

const ptySupported = false

type ptyProcess struct{ pid int }

func startPty(*ptyRequest) (*ptyProcess, *Failure) {
	return nil, newFailure("ENOTSUP", "Terminals are not supported on this system yet")
}

func (*ptyProcess) setSize(uint16, uint16) error            { return nil }
func (*ptyProcess) foreground() (int, error)                { return 0, nil }
func (*ptyProcess) killGroup()                              {}
func (*ptyProcess) wait() (int, error)                      { return 0, nil }
func (*ptyProcess) read(<-chan struct{}, func([]byte) bool) {}
func (*ptyProcess) writeAll([]byte, <-chan struct{}) bool   { return false }
func (*ptyProcess) close()                                  {}

func processName(int) (string, bool) { return "", false }
