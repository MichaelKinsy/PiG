//go:build !(gc && (linux || darwin) && (amd64 || arm64))

package goroutineid

// current parses the stack header: this platform does not read the runtime's goroutine record.
func current() uint64 { return stackID() }
